package bakpack

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// batchTestSamples returns original Bakta JSON for three samples covering the
// older layout, the Bakta >= 1.10 layout and renamed contigs, with matching
// genomes.
func batchTestSamples() (map[string][]byte, map[string][]byte) {
	annotations := map[string][]byte{
		"sampleA": toyBaktaJSON("sampleA", "gene A"),
		"sampleB": toyBaktaJSONv110("sampleB", "gene B"),
		"sampleC": renamedContigBaktaJSON(),
	}
	genomes := map[string][]byte{
		"sampleA": toyFASTA("sampleA"),
		"sampleB": toyFASTA("sampleB"),
		"sampleC": renamedContigFASTA(),
	}
	return annotations, genomes
}

// writeBatchTestSources writes the samples as an annotation tar and a genome
// tar in a different order, so pairing has to spool.
func writeBatchTestSources(t *testing.T, dir string) (FileSource, FileSource) {
	t.Helper()
	annotations, genomes := batchTestSamples()
	var annotationEntries, genomeEntries []tarEntry
	for _, sample := range []string{"sampleA", "sampleB", "sampleC"} {
		annotationEntries = append(annotationEntries, tarEntry{Name: "bakta/" + sample + ".bakta.json", Data: annotations[sample]})
	}
	for _, sample := range []string{"sampleC", "sampleA", "sampleB"} {
		genomeEntries = append(genomeEntries, tarEntry{Name: "genomes/" + sample + ".fa", Data: genomes[sample]})
	}
	writeTar(t, filepath.Join(dir, "annotations.tar"), annotationEntries)
	writeTar(t, filepath.Join(dir, "genomes.tar"), genomeEntries)
	return TarSource{Path: filepath.Join(dir, "annotations.tar"), Role: "annotation"},
		TarSource{Path: filepath.Join(dir, "genomes.tar"), Role: "genome"}
}

// readBatchOutput returns the files a batch wrote, in order, keyed by name.
func readBatchOutput(t *testing.T, path string) ([]string, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{}
	var names []string
	if !isTarPath(path) {
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, entry.Name())
			files[entry.Name()] = data
		}
		return names, files
	}
	// Read raw members: the sample-aware tar reader skips non-JSON names.
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	r, closeDecoder, err := tarDecoder(tarSource{Path: path, Compression: tarCompressionFromPath(path)}, file)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDecoder()
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		files[header.Name] = data
	}
	return names, files
}

func TestReduceRestoreAndGFF3Batch(t *testing.T) {
	originals, genomeFASTAs := batchTestSamples()
	for _, output := range []string{"reduced", "reduced.tar", "reduced.tar.gz", "reduced.tar.bz2", "reduced.tar.xz", "reduced.tar.zst"} {
		t.Run(output, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			annotations, genomes := writeBatchTestSources(t, dir)
			reducedPath := filepath.Join(dir, "out", output)
			if err := os.MkdirAll(filepath.Dir(reducedPath), 0o755); err != nil {
				t.Fatal(err)
			}

			var warnings bytes.Buffer
			if err := ReduceBatch(ctx, BatchOptions{Annotations: annotations, Genomes: genomes, OutputPath: reducedPath, Warnings: &warnings}); err != nil {
				t.Fatalf("ReduceBatch() error = %v", err)
			}
			if warnings.Len() != 0 {
				t.Fatalf("ReduceBatch() warnings = %q, want none", warnings.String())
			}
			names, reducedFiles := readBatchOutput(t, reducedPath)
			wantNames := "sampleA.reduced.bakta.json,sampleB.reduced.bakta.json,sampleC.reduced.bakta.json"
			if isTarPath(output) {
				// Tar members follow genome order.
				wantNames = "sampleC.reduced.bakta.json,sampleA.reduced.bakta.json,sampleB.reduced.bakta.json"
			}
			if strings.Join(names, ",") != wantNames {
				t.Fatalf("reduced outputs = %v, want %s", names, wantNames)
			}
			for name, data := range reducedFiles {
				if !bytes.Contains(data, []byte(`"_bakpack"`)) || bytes.Contains(data, []byte(`"aa": "`)) {
					t.Fatalf("%s is not reduced JSON", name)
				}
			}
			entries, err := os.ReadDir(filepath.Dir(reducedPath))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("output directory has %d entries, want just %s: %v", len(entries), output, entries)
			}

			// Restore and GFF3 read the reduced output back as their source.
			reducedSource, err := OpenSource(reducedPath, "auto", "annotation")
			if err != nil {
				t.Fatal(err)
			}
			restoredPath := filepath.Join(dir, "restored")
			if err := RestoreBatch(ctx, BatchOptions{Annotations: reducedSource, Genomes: genomes, OutputPath: restoredPath}); err != nil {
				t.Fatalf("RestoreBatch() error = %v", err)
			}
			gff3Path := filepath.Join(dir, "gff3.tar.gz")
			if err := GFF3Batch(ctx, BatchOptions{Annotations: reducedSource, Genomes: genomes, OutputPath: gff3Path, GFF3AnnotationOnly: true}); err != nil {
				t.Fatalf("GFF3Batch() error = %v", err)
			}
			_, gff3Files := readBatchOutput(t, gff3Path)
			for sample, original := range originals {
				restored, err := os.ReadFile(filepath.Join(restoredPath, sample+".bakta.json"))
				if err != nil {
					t.Fatal(err)
				}
				gotCanonical, err := JSONBytesCanonicalSHA256(restored)
				if err != nil {
					t.Fatal(err)
				}
				wantCanonical, err := JSONBytesCanonicalSHA256(original)
				if err != nil {
					t.Fatal(err)
				}
				if gotCanonical != wantCanonical {
					t.Fatalf("%s restored canonical SHA = %s, want %s", sample, gotCanonical, wantCanonical)
				}
				wantGFF3, err := BaktaGFF3WithOptions(original, mustGenome(t, sample, genomeFASTAs[sample]), BaktaGFF3Options{AnnotationOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(gff3Files[sample+".gff3"], wantGFF3) {
					t.Fatalf("%s GFF3 from reduced batch differs from GFF3 of the original", sample)
				}
			}
		})
	}
}

func TestBatchFollowsOrder(t *testing.T) {
	dir := t.TempDir()
	annotations, genomes := writeBatchTestSources(t, dir)
	outPath := filepath.Join(dir, "reduced.tar")
	if err := ReduceBatch(context.Background(), BatchOptions{
		Annotations: annotations,
		Genomes:     genomes,
		Order:       []string{"sampleB", "sampleC", "sampleA"},
		OutputPath:  outPath,
	}); err != nil {
		t.Fatalf("ReduceBatch() error = %v", err)
	}
	names, _ := readBatchOutput(t, outPath)
	if got, want := strings.Join(names, ","), "sampleB.reduced.bakta.json,sampleC.reduced.bakta.json,sampleA.reduced.bakta.json"; got != want {
		t.Fatalf("output order = %s, want %s", got, want)
	}
}

func TestBatchFailureLeavesNoTar(t *testing.T) {
	dir := t.TempDir()
	annotations, _ := writeBatchTestSources(t, dir)
	// sampleB's genome is missing.
	writeTar(t, filepath.Join(dir, "partial.tar"), []tarEntry{
		{Name: "sampleA.fa", Data: toyFASTA("sampleA")},
		{Name: "sampleC.fa", Data: renamedContigFASTA()},
	})
	outDir := filepath.Join(dir, "out")
	if err := os.Mkdir(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ReduceBatch(context.Background(), BatchOptions{
		Annotations: annotations,
		Genomes:     TarSource{Path: filepath.Join(dir, "partial.tar"), Role: "genome"},
		OutputPath:  filepath.Join(outDir, "reduced.tar.gz"),
	})
	if err == nil || !strings.Contains(err.Error(), `"sampleB"`) {
		t.Fatalf("ReduceBatch() error = %v, want missing sampleB", err)
	}
	entries, readErr := os.ReadDir(outDir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed batch left %v in the output directory", entries)
	}
}

func TestReduceBatchWarnsOnUnmatchedSequences(t *testing.T) {
	dir := t.TempDir()
	writeTar(t, filepath.Join(dir, "annotations.tar"), []tarEntry{{Name: "sampleC.bakta.json", Data: renamedContigBaktaJSON()}})
	writeTar(t, filepath.Join(dir, "genomes.tar"), []tarEntry{{Name: "sampleC.fa", Data: []byte(">other\nTTGAAATAAATGAAATAA\n")}})
	var warnings bytes.Buffer
	if err := ReduceBatch(context.Background(), BatchOptions{
		Annotations: TarSource{Path: filepath.Join(dir, "annotations.tar"), Role: "annotation"},
		Genomes:     TarSource{Path: filepath.Join(dir, "genomes.tar"), Role: "genome"},
		OutputPath:  filepath.Join(dir, "reduced"),
		Warnings:    &warnings,
	}); err != nil {
		t.Fatalf("ReduceBatch() error = %v", err)
	}
	if !strings.Contains(warnings.String(), "warning: sampleC: 1 sequence(s) not found") {
		t.Fatalf("ReduceBatch() warnings = %q, want unmatched sequence warning", warnings.String())
	}
}

func TestSampleIDFromNameStripsReducedSuffix(t *testing.T) {
	for name, want := range map[string]string{
		"sampleA.reduced.bakta.json": "sampleA",
		"sampleA.reduced.json":       "sampleA",
		"sampleA.bakta.json":         "sampleA",
		"sampleA.json":               "sampleA",
	} {
		if got := sampleIDFromName(name, "annotation"); got != want {
			t.Fatalf("sampleIDFromName(%q) = %q, want %q", name, got, want)
		}
	}
}
