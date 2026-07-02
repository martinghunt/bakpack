package bakpack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
