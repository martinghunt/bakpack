package bakpack

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dsnetbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func TestListSourcePathOnlyLinesAllowSpacesInPaths(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	genomesDir := filepath.Join(dir, "genome files")
	if err := os.Mkdir(genomesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(genomesDir, "sampleA.fa"), toyFASTA("sampleA"))
	genomeList := filepath.Join(dir, "genomes.list")
	writeFile(t, genomeList, []byte("genome files/sampleA.fa\n"))

	genomes, err := OpenSource(genomeList, "list", "genome")
	if err != nil {
		t.Fatal(err)
	}
	records, err := genomes.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].SampleID != "sampleA" || records[0].Name != "sampleA.fa" {
		t.Fatalf("records = %#v, want sampleA from path with spaces", records)
	}
}

func TestAGCGenomeSourceGetsetArgsUseOneThreadByDefault(t *testing.T) {
	source := AGCGenomeSource{Path: "genomes.agc"}
	if got, want := strings.Join(source.getsetArgs("sampleA"), " "), "getset -t 1 genomes.agc sampleA"; got != want {
		t.Fatalf("default getset args = %q, want %q", got, want)
	}

	source.Threads = 4
	if got, want := strings.Join(source.getsetArgs("sampleA"), " "), "getset -t 4 genomes.agc sampleA"; got != want {
		t.Fatalf("overridden getset args = %q, want %q", got, want)
	}
}

func TestOpenSourceGenomeFASTAFileSupportsFAQTCompression(t *testing.T) {
	ctx := context.Background()
	for _, format := range []struct {
		name   string
		suffix string
		pack   func(*testing.T, []byte) []byte
	}{
		{name: "gzip", suffix: ".fa.gz", pack: gzipBytes},
		{name: "bzip2", suffix: ".fasta.bz2", pack: bzip2Bytes},
		{name: "xz", suffix: ".fna.xz", pack: xzBytes},
		{name: "zstd", suffix: ".fa.zst", pack: zstdBytes},
	} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sampleA"+format.suffix)
			writeFile(t, path, format.pack(t, toyFASTA("sampleA")))

			source, err := OpenSource(path, "auto", "genome")
			if err != nil {
				t.Fatal(err)
			}
			record, err := source.Get(ctx, "sampleA")
			if err != nil {
				t.Fatal(err)
			}
			genome, err := ReadGenome(record.SampleID, record.Name, record.Bytes)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(genome.Contigs[0].Seq); got != "ATGAAATAA" {
				t.Fatalf("genome sequence = %q", got)
			}
		})
	}
}

func TestDirSourceGenomeFASTARecognisesCompressedNames(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sampleA.fa.gz"), gzipBytes(t, toyFASTA("sampleA")))

	source, err := OpenSource(dir, "auto", "genome")
	if err != nil {
		t.Fatal(err)
	}
	record, err := source.Get(ctx, "sampleA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGenome(record.SampleID, record.Name, record.Bytes); err != nil {
		t.Fatalf("ReadGenome() error = %v", err)
	}
}

func gzipBytes(t *testing.T, input []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func bzip2Bytes(t *testing.T, input []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := dsnetbzip2.NewWriter(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func xzBytes(t *testing.T, input []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zstdBytes(t *testing.T, input []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
