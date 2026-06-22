package bakpack

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

func TestReduceAndRestoreBaktaJSONChecksCanonicalContent(t *testing.T) {
	genome := mustGenome(t, "sample1", toyFASTA("sample1"))
	original := toyBaktaJSON("sample1", "gene one")

	reduced, err := ReduceBaktaJSON(original, genome)
	if err != nil {
		t.Fatalf("ReduceBaktaJSON() error = %v", err)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"nt"`)) {
		t.Fatalf("reduced JSON still contains nt: %s", reduced.ReducedJSON)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"aa"`)) {
		t.Fatalf("reduced JSON still contains aa: %s", reduced.ReducedJSON)
	}
	if bytes.Contains(reduced.ReducedJSON, []byte(`"sequence"`)) {
		t.Fatalf("reduced JSON still contains sequence: %s", reduced.ReducedJSON)
	}
	if reduced.Original.BytesSHA256 != SHA256Hex(original) {
		t.Fatalf("original byte SHA mismatch")
	}

	restored, err := RestoreBaktaJSON(reduced.ReducedJSON, genome)
	if err != nil {
		t.Fatalf("RestoreBaktaJSON() error = %v", err)
	}
	wantCanonical, err := JSONBytesCanonicalSHA256(original)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Original.CanonicalSHA256 != wantCanonical {
		t.Fatalf("restored canonical SHA = %s, want %s", restored.Original.CanonicalSHA256, wantCanonical)
	}
}

func TestBuildAndExtractArchiveFromTarXZUsesGenomeArchiveOrder(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	annotationsTar := filepath.Join(dir, "annotations.tar.xz")
	genomesTar := filepath.Join(dir, "genomes.tar.xz")
	archivePath := filepath.Join(dir, "toy.bakpack")
	outDir := filepath.Join(dir, "out")

	writeTarXZ(t, genomesTar, []tarEntry{
		{Name: "genomes/sampleB.fa", Data: toyFASTA("sampleB")},
		{Name: "genomes/sampleA.fa", Data: toyFASTA("sampleA")},
	})
	writeTarXZ(t, annotationsTar, []tarEntry{
		{Name: "bakta/sampleA.bakta.json", Data: toyBaktaJSON("sampleA", "gene A")},
		{Name: "bakta/sampleB.bakta.json", Data: toyBaktaJSON("sampleB", "gene B")},
	})

	annotations, err := OpenSource(annotationsTar, "tar.xz", "annotation")
	if err != nil {
		t.Fatal(err)
	}
	genomes, err := OpenSource(genomesTar, "tar.xz", "genome")
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildArchive(ctx, BuildOptions{
		Annotations: annotations,
		Genomes:     genomes,
		ChunkSize:   2,
		OutputPath:  archivePath,
	}); err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}

	index, err := ReadArchiveIndex(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{index.Samples[0].SampleID, index.Samples[1].SampleID}; got[0] != "sampleB" || got[1] != "sampleA" {
		t.Fatalf("archive sample order = %v, want [sampleB sampleA]", got)
	}
	if index.Samples[0].OriginalJSONCanonicalSHA256 == "" || index.Samples[0].ReducedJSONCanonicalSHA256 == "" {
		t.Fatalf("index missing JSON checksums: %#v", index.Samples[0])
	}

	if err := ExtractArchive(ctx, ExtractOptions{
		ArchivePath: archivePath,
		Genomes:     genomes,
		Samples:     []string{"sampleA", "sampleB"},
		OutputDir:   outDir,
		Reduced:     true,
		Original:    true,
		Genome:      true,
	}); err != nil {
		t.Fatalf("ExtractArchive() error = %v", err)
	}

	for _, sample := range []string{"sampleA", "sampleB"} {
		originalOut, err := os.ReadFile(filepath.Join(outDir, sample+".bakta.json"))
		if err != nil {
			t.Fatal(err)
		}
		gotCanonical, err := JSONBytesCanonicalSHA256(originalOut)
		if err != nil {
			t.Fatal(err)
		}
		wantCanonical, err := JSONBytesCanonicalSHA256(toyBaktaJSON(sample, "gene "+sample[len(sample)-1:]))
		if err != nil {
			t.Fatal(err)
		}
		if gotCanonical != wantCanonical {
			t.Fatalf("%s original canonical SHA = %s, want %s", sample, gotCanonical, wantCanonical)
		}
		genomeOut, err := os.ReadFile(filepath.Join(outDir, sample+".fa"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(genomeOut, []byte(">contig1")) || !bytes.Contains(genomeOut, []byte("ATGAAATAA")) {
			t.Fatalf("%s genome FASTA not written correctly: %s", sample, genomeOut)
		}
	}
}

func TestBuildArchiveFromDirectoryAnnotationsAndGenomeList(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	annotationsDir := filepath.Join(dir, "annotations")
	genomesDir := filepath.Join(dir, "genomes")
	if err := os.Mkdir(annotationsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(genomesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(annotationsDir, "sampleA.bakta.json"), toyBaktaJSON("sampleA", "gene A"))
	writeFile(t, filepath.Join(annotationsDir, "sampleB.bakta.json"), toyBaktaJSON("sampleB", "gene B"))
	writeFile(t, filepath.Join(genomesDir, "sampleA.fa"), toyFASTA("sampleA"))
	writeFile(t, filepath.Join(genomesDir, "sampleB.fa"), toyFASTA("sampleB"))
	genomeList := filepath.Join(dir, "genomes.list")
	writeFile(t, genomeList, []byte("sampleB\tgenomes/sampleB.fa\nsampleA\tgenomes/sampleA.fa\n"))

	annotations, err := OpenSource(annotationsDir, "dir", "annotation")
	if err != nil {
		t.Fatal(err)
	}
	genomes, err := OpenSource(genomeList, "list", "genome")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(dir, "from-list.bakpack")
	if err := BuildArchive(ctx, BuildOptions{
		Annotations: annotations,
		Genomes:     genomes,
		ChunkSize:   1,
		OutputPath:  archivePath,
	}); err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}
	index, err := ReadArchiveIndex(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{index.Samples[0].SampleID, index.Samples[1].SampleID}; got[0] != "sampleB" || got[1] != "sampleA" {
		t.Fatalf("archive sample order = %v, want genome list order", got)
	}
}

func TestXZCompressDefaultsToOneThreadAndAllowsOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script")
	}
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "xz.args")
	fakeXZ := filepath.Join(dir, "xz")
	writeFile(t, fakeXZ, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$BAKPACK_XZ_ARGS\"\ncat\n"))
	if err := os.Chmod(fakeXZ, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BAKPACK_XZ_ARGS", argsPath)

	got, err := xzCompress([]byte("payload"), BuildOptions{XZCommand: fakeXZ})
	if err != nil {
		t.Fatalf("xzCompress() error = %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("xzCompress() output = %q", got)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "-9e\n-T1\n-c" {
		t.Fatalf("default xz args = %q, want -9e -T1 -c", args)
	}

	if _, err := xzCompress([]byte("payload"), BuildOptions{XZCommand: fakeXZ, XZThreads: 4}); err != nil {
		t.Fatalf("xzCompress() with XZThreads error = %v", err)
	}
	args, err = os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "-9e\n-T4\n-c" {
		t.Fatalf("thread override xz args = %q, want -9e -T4 -c", args)
	}
}

type tarEntry struct {
	Name string
	Data []byte
}

func writeTarXZ(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	xzw, err := xz.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xzw)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.Name, Mode: 0o644, Size: int64(len(entry.Data))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xzw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGenome(t *testing.T, sample string, data []byte) Genome {
	t.Helper()
	genome, err := ReadGenome(sample, sample+".fa", data)
	if err != nil {
		t.Fatal(err)
	}
	return genome
}

func toyFASTA(sample string) []byte {
	return []byte(">contig1 " + sample + "\nATGAAATAA\n")
}

func toyBaktaJSON(sample, product string) []byte {
	_ = sample
	return []byte(`{
  "genome": {
    "translation_table": 11
  },
  "sequences": [
    {
      "id": "contig1",
      "description": "toy contig",
      "sequence": "ATGAAATAA"
    }
  ],
  "features": [
    {
      "type": "cds",
      "contig": "contig1",
      "start": 1,
      "stop": 9,
      "strand": "+",
      "product": "` + product + `",
      "nt": "ATGAAATAA",
      "aa": "MK",
      "id": "toy_00001"
    }
  ]
}
`)
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
