package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/martinghunt/bakpack"
)

func TestCLIWorkflowWithDirectoryInputs(t *testing.T) {
	dir := t.TempDir()
	annotationsDir := filepath.Join(dir, "annotations")
	genomesDir := filepath.Join(dir, "genomes")
	outDir := filepath.Join(dir, "out")
	mustMkdir(t, annotationsDir)
	mustMkdir(t, genomesDir)
	originalJSON := toyBaktaJSON("cli sample")
	writeFile(t, filepath.Join(annotationsDir, "sample1.bakta.json"), originalJSON)
	writeFile(t, filepath.Join(genomesDir, "sample1.fa"), toyFASTA())

	reducedPath := filepath.Join(dir, "sample1.reduced.json")
	_, stderr, err := executeCommand("reduce", filepath.Join(annotationsDir, "sample1.bakta.json"), filepath.Join(genomesDir, "sample1.fa"), "-o", reducedPath)
	if err != nil {
		t.Fatalf("reduce command error = %v, stderr = %s", err, stderr)
	}
	if !bytes.Contains(stderr, []byte("original_json_canonical_sha256")) {
		t.Fatalf("reduce stderr missing checksum output: %s", stderr)
	}

	restoredPath := filepath.Join(dir, "sample1.restored.json")
	_, stderr, err = executeCommand("restore", reducedPath, filepath.Join(genomesDir, "sample1.fa"), "-o", restoredPath)
	if err != nil {
		t.Fatalf("restore command error = %v, stderr = %s", err, stderr)
	}
	assertCanonicalEqual(t, restoredPath, originalJSON)

	archivePath := filepath.Join(dir, "annotations.bakpack")
	_, stderr, err = executeCommand(
		"build",
		"--annotations", annotationsDir,
		"--genomes", genomesDir,
		"--output", archivePath,
		"--chunk-size", "1",
	)
	if err != nil {
		t.Fatalf("build command error = %v, stderr = %s", err, stderr)
	}

	_, stderr, err = executeCommand(
		"extract",
		archivePath,
		"sample1",
		"--genomes", genomesDir,
		"--output-dir", outDir,
		"--original",
		"--genome",
	)
	if err != nil {
		t.Fatalf("extract command error = %v, stderr = %s", err, stderr)
	}
	assertCanonicalEqual(t, filepath.Join(outDir, "sample1.bakta.json"), originalJSON)
	genomeOut, err := os.ReadFile(filepath.Join(outDir, "sample1.fa"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(genomeOut, []byte("ATGAAATAA")) {
		t.Fatalf("extracted genome FASTA = %s", genomeOut)
	}
}

func TestCLIBuildWithCombinedManifest(t *testing.T) {
	dir := t.TempDir()
	annotationsDir := filepath.Join(dir, "annotation files")
	genomesDir := filepath.Join(dir, "genome files")
	outDir := filepath.Join(dir, "out")
	mustMkdir(t, annotationsDir)
	mustMkdir(t, genomesDir)
	originalJSON := toyBaktaJSON("manifest sample")
	writeFile(t, filepath.Join(annotationsDir, "sample annotation.json"), originalJSON)
	writeFile(t, filepath.Join(genomesDir, "sample genome.fa"), toyFASTA())
	manifestPath := filepath.Join(dir, "manifest.tsv")
	writeFile(t, manifestPath, []byte("sampleX\tannotation files/sample annotation.json\tgenome files/sample genome.fa\n"))

	archivePath := filepath.Join(dir, "manifest.bakpack")
	_, stderr, err := executeCommand(
		"build",
		"--manifest", manifestPath,
		"--output", archivePath,
		"--chunk-size", "1",
	)
	if err != nil {
		t.Fatalf("build --manifest command error = %v, stderr = %s", err, stderr)
	}

	_, stderr, err = executeCommand(
		"extract",
		archivePath,
		"sampleX",
		"--genomes", manifestPath,
		"--genomes-format", "manifest",
		"--output-dir", outDir,
		"--original",
	)
	if err != nil {
		t.Fatalf("extract command error = %v, stderr = %s", err, stderr)
	}
	assertCanonicalEqual(t, filepath.Join(outDir, "sampleX.bakta.json"), originalJSON)
}

func executeCommand(args ...string) ([]byte, []byte, error) {
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.Bytes(), stderr.Bytes(), err
}

func assertCanonicalEqual(t *testing.T, gotPath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	gotHash, err := bakpack.JSONBytesCanonicalSHA256(got)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := bakpack.JSONBytesCanonicalSHA256(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHash {
		t.Fatalf("%s canonical SHA = %s, want %s", gotPath, gotHash, wantHash)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func toyFASTA() []byte {
	return []byte(">contig1\nATGAAATAA\n")
}

func toyBaktaJSON(product string) []byte {
	return []byte(`{
  "genome": {
    "translation_table": 11
  },
  "sequences": [
    {
      "id": "contig1",
      "length": 9,
      "sequence": "ATGAAATAA"
    }
  ],
  "stats": {
    "no_sequences": 1,
    "size": 9,
    "n_ratio": 0.0,
    "n50": 9
  },
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
      "aa_hexdigest": "fbd1e7ba9564863b88d5c43cb833afaf",
      "start_type": "ATG"
    }
  ]
}
`)
}
