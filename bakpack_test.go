package bakpack

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	for _, key := range []string{`"aa_hexdigest"`, `"start_type"`, `"length"`, `"no_sequences"`, `"n50"`} {
		if bytes.Contains(reduced.ReducedJSON, []byte(key)) {
			t.Fatalf("reduced JSON still contains derivable field %s: %s", key, reduced.ReducedJSON)
		}
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(`"_bakpack"`)) {
		t.Fatalf("reduced JSON missing bakpack metadata: %s", reduced.ReducedJSON)
	}
	if !bytes.Contains(reduced.ReducedJSON, []byte(reduced.Original.CanonicalSHA256)) {
		t.Fatalf("reduced JSON missing original canonical checksum: %s", reduced.ReducedJSON)
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

	tampered := bytes.Replace(reduced.ReducedJSON, []byte(reduced.Original.CanonicalSHA256), []byte(strings.Repeat("0", 64)), 1)
	if _, err := RestoreBaktaJSON(tampered, genome); err == nil || !strings.Contains(err.Error(), "original_json_canonical_sha256 mismatch") {
		t.Fatalf("RestoreBaktaJSON() with bad embedded checksum error = %v, want checksum mismatch", err)
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
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	indexLen := binary.LittleEndian.Uint64(archiveBytes[len(ArchiveMagic) : len(ArchiveMagic)+8])
	indexStart := len(ArchiveMagic) + 8
	if !isXZ(archiveBytes[indexStart : indexStart+int(indexLen)]) {
		t.Fatalf("archive index is not xz-compressed")
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
	if index.PayloadFormat != optimizedPayloadFormat {
		t.Fatalf("payload format = %q, want %q", index.PayloadFormat, optimizedPayloadFormat)
	}
	if len(index.Chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(index.Chunks))
	}
	codecs := map[string]string{}
	for _, codec := range index.Chunks[0].FieldCodecs {
		codecs[codec.Field] = codec.Kind
	}
	if codecs["contig"] != "sequence_index" {
		t.Fatalf("contig codec = %q, want sequence_index", codecs["contig"])
	}
	if codecs["id"] != "sample_prefix_uint_string" {
		t.Fatalf("id codec = %q, want sample_prefix_uint_string", codecs["id"])
	}
	if codecs["start"] != "uint" || codecs["stop"] != "uint" {
		t.Fatalf("coordinate codecs = start:%q stop:%q, want uint", codecs["start"], codecs["stop"])
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

func TestExtractArchiveFromHTTPRangeURL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	annotationsDir := filepath.Join(dir, "annotations")
	genomesDir := filepath.Join(dir, "genomes")
	outDir := filepath.Join(dir, "out")
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

	annotations, err := OpenSource(annotationsDir, "dir", "annotation")
	if err != nil {
		t.Fatal(err)
	}
	genomes, err := OpenSource(genomesDir, "dir", "genome")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(dir, "http.bakpack")
	if err := BuildArchive(ctx, BuildOptions{
		Annotations: annotations,
		Genomes:     genomes,
		Order:       []string{"sampleA", "sampleB"},
		ChunkSize:   1,
		OutputPath:  archivePath,
	}); err != nil {
		t.Fatalf("BuildArchive() error = %v", err)
	}

	var mu sync.Mutex
	var ranges []string
	nonRangeRequests := 0
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local HTTP listener unavailable: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			nonRangeRequests++
		} else {
			ranges = append(ranges, rangeHeader)
		}
		mu.Unlock()
		http.ServeFile(w, r, archivePath)
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()

	index, err := ReadArchiveIndex(server.URL)
	if err != nil {
		t.Fatalf("ReadArchiveIndex(%q) error = %v", server.URL, err)
	}
	if len(index.Samples) != 2 {
		t.Fatalf("HTTP archive index sample count = %d, want 2", len(index.Samples))
	}

	if err := ExtractArchive(ctx, ExtractOptions{
		ArchivePath: server.URL,
		Genomes:     genomes,
		Samples:     []string{"sampleB"},
		OutputDir:   outDir,
		Reduced:     true,
		Original:    true,
	}); err != nil {
		t.Fatalf("ExtractArchive() from HTTP URL error = %v", err)
	}
	assertCanonicalFileEqual(t, filepath.Join(outDir, "sampleB.bakta.json"), toyBaktaJSON("sampleB", "gene B"))

	mu.Lock()
	defer mu.Unlock()
	if nonRangeRequests != 0 {
		t.Fatalf("HTTP archive reader made %d non-range requests", nonRangeRequests)
	}
	if len(ranges) < 5 {
		t.Fatalf("HTTP archive reader made %d range requests, want at least 5; ranges=%v", len(ranges), ranges)
	}
	for _, rangeHeader := range ranges {
		if !strings.HasPrefix(rangeHeader, "bytes=") {
			t.Fatalf("bad range header %q", rangeHeader)
		}
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
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := xzCompress([]byte("payload"), BuildOptions{})
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

	if _, err := xzCompress([]byte("payload"), BuildOptions{XZThreads: 4}); err != nil {
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

func assertCanonicalFileEqual(t *testing.T, gotPath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatal(err)
	}
	gotCanonical, err := JSONBytesCanonicalSHA256(got)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical, err := JSONBytesCanonicalSHA256(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotCanonical != wantCanonical {
		t.Fatalf("%s canonical SHA = %s, want %s", gotPath, gotCanonical, wantCanonical)
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
      "length": 9,
      "sequence": "ATGAAATAA"
    }
  ],
  "stats": {
    "no_sequences": 1,
    "size": 9,
    "gc": 33.333333333333336,
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
      "start_type": "ATG",
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
