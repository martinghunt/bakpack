package bakpack

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
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
	if got, want := strings.Join(source.getsetArgs("sampleA"), " "), "getset -t 1 -- genomes.agc sampleA"; got != want {
		t.Fatalf("default getset args = %q, want %q", got, want)
	}

	source.Threads = 4
	if got, want := strings.Join(source.getsetArgs("sampleA"), " "), "getset -t 4 -- genomes.agc sampleA"; got != want {
		t.Fatalf("overridden getset args = %q, want %q", got, want)
	}
}

func TestAGCGenomeSourceGetsetArgsProtectSampleNamesStartingWithDash(t *testing.T) {
	source := AGCGenomeSource{Path: "genomes.agc"}
	if got, want := strings.Join(source.getsetArgs("-sampleA"), " "), "getset -t 1 -- genomes.agc -sampleA"; got != want {
		t.Fatalf("getset args for dash-prefixed sample = %q, want %q", got, want)
	}
}

func TestAGCGenomeSourceListsetArgsProtectPathStartingWithDash(t *testing.T) {
	source := AGCGenomeSource{Path: "-genomes.agc"}
	if got, want := strings.Join(source.listsetArgs(), " "), "listset -- -genomes.agc"; got != want {
		t.Fatalf("listset args for dash-prefixed path = %q, want %q", got, want)
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

func TestOpenSourceReadsPlainTar(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	annotationsPath := filepath.Join(dir, "annotations.tar")
	genomesPath := filepath.Join(dir, "genomes.tar")
	writeTar(t, annotationsPath, []tarEntry{
		{Name: "bakta/sampleB.bakta.json", Data: toyBaktaJSON("sampleB", "gene B")},
		{Name: "bakta/README.txt", Data: []byte("not a sample")},
		{Name: "bakta/sampleA.bakta.json", Data: toyBaktaJSON("sampleA", "gene A")},
	})
	writeTar(t, genomesPath, []tarEntry{
		{Name: "genomes/sampleA.fa.gz", Data: gzipBytes(t, toyFASTA("sampleA"))},
	})

	annotations, err := OpenSource(annotationsPath, "auto", "annotation")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := annotations.(TarSource); !ok {
		t.Fatalf("OpenSource(.tar) = %T, want TarSource", annotations)
	}
	order, err := annotations.Order(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "sampleB,sampleA" {
		t.Fatalf("Order() = %v, want tar order [sampleB sampleA]", order)
	}
	record, err := annotations.Get(ctx, "sampleA")
	if err != nil {
		t.Fatal(err)
	}
	if record.Name != "bakta/sampleA.bakta.json" || !bytes.Equal(record.Bytes, toyBaktaJSON("sampleA", "gene A")) {
		t.Fatalf("Get(sampleA) = %q with %d bytes", record.Name, len(record.Bytes))
	}

	genomes, err := OpenSource(genomesPath, "tar", "genome")
	if err != nil {
		t.Fatal(err)
	}
	genomeRecord, err := genomes.Get(ctx, "sampleA")
	if err != nil {
		t.Fatal(err)
	}
	genome, err := ReadGenome(genomeRecord.SampleID, genomeRecord.Name, genomeRecord.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(genome.Contigs[0].Seq); got != "ATGAAATAA" {
		t.Fatalf("genome sequence from compressed tar member = %q", got)
	}
}

func TestOpenSourceDetectsTarCompressionFromSuffix(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name        string
		compression string
		wantType    string
	}{
		{name: "annotations.tar", compression: "", wantType: "TarSource"},
		{name: "annotations.tar.gz", compression: "gz", wantType: "TarSource"},
		{name: "annotations.tgz", compression: "gz", wantType: "TarSource"},
		{name: "annotations.TAR.GZ", compression: "gz", wantType: "TarSource"},
		{name: "annotations.tar.bz2", compression: "bz2", wantType: "TarSource"},
		{name: "annotations.tbz2", compression: "bz2", wantType: "TarSource"},
		{name: "annotations.tar.xz", compression: "xz", wantType: "TarXZSource"},
		{name: "annotations.txz", compression: "xz", wantType: "TarXZSource"},
		{name: "annotations.tar.zst", compression: "zst", wantType: "TarSource"},
		{name: "annotations.tzst", compression: "zst", wantType: "TarSource"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.name)
			writeTarArchive(t, path, []tarEntry{
				{Name: "sampleB.bakta.json", Data: toyBaktaJSON("sampleB", "gene B")},
				{Name: "sampleA.bakta.json", Data: toyBaktaJSON("sampleA", "gene A")},
			}, tt.compression)

			source, err := OpenSource(path, "auto", "annotation")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimPrefix(fmt.Sprintf("%T", source), "bakpack."); got != tt.wantType {
				t.Fatalf("OpenSource() = %s, want %s", got, tt.wantType)
			}
			if tarSource, ok := source.(TarSource); ok && tarSource.Compression != tt.compression {
				t.Fatalf("TarSource.Compression = %q, want %q", tarSource.Compression, tt.compression)
			}
			order, err := source.Order(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(order, ",") != "sampleB,sampleA" {
				t.Fatalf("Order() = %v, want [sampleB sampleA]", order)
			}
			record, err := source.Get(ctx, "sampleA")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(record.Bytes, toyBaktaJSON("sampleA", "gene A")) {
				t.Fatalf("Get(sampleA) returned %d unexpected bytes", len(record.Bytes))
			}
		})
	}
}

func TestOpenSourceExplicitTarFormat(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		format      string
		compression string
	}{
		{format: "tar.gz", compression: "gz"},
		{format: "tgz", compression: "gz"},
		{format: "tar.bz2", compression: "bz2"},
		{format: "tar.zst", compression: "zst"},
		{format: "tar", compression: ""},
	} {
		t.Run(tt.format, func(t *testing.T) {
			// No recognisable suffix, so only the format names the compression.
			path := filepath.Join(t.TempDir(), "genomes.bin")
			writeTarArchive(t, path, []tarEntry{{Name: "sampleA.fa", Data: toyFASTA("sampleA")}}, tt.compression)
			source, err := OpenSource(path, tt.format, "genome")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Get(ctx, "sampleA"); err != nil {
				t.Fatalf("Get(sampleA) error = %v", err)
			}
		})
	}
}

func TestTarSourceRejectsWrongCompression(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "annotations.tar.gz")
	writeTarArchive(t, path, []tarEntry{{Name: "sampleA.bakta.json", Data: toyBaktaJSON("sampleA", "gene A")}}, "zst")
	source, err := OpenSource(path, "auto", "annotation")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Order(ctx); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Order() on zstd data named .tar.gz error = %v, want error naming the file", err)
	}
	unknown := TarSource{Path: path, Role: "annotation", Compression: "lz4"}
	if _, err := unknown.Order(ctx); err == nil || !strings.Contains(err.Error(), `unsupported tar compression "lz4"`) {
		t.Fatalf("Order() with unknown compression error = %v, want unsupported compression", err)
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
