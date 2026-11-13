package bakpack

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/martinghunt/faqt/agc"
	"github.com/ulikunitz/xz"
)

type FileRecord struct {
	SampleID string
	Name     string
	Bytes    []byte
}

type FileSource interface {
	Records(context.Context) ([]FileRecord, error)
	Get(context.Context, string) (FileRecord, error)
	Order(context.Context) ([]string, error)
}

type ManifestEntry struct {
	SampleID       string
	AnnotationPath string
	GenomePath     string
}

type Manifest struct {
	Path    string
	Entries []ManifestEntry
}

func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	base := filepath.Dir(path)
	manifest := Manifest{Path: path}
	seen := map[string]bool{}
	seenDataLine := false
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitTabFields(line)
		if len(fields) != 3 {
			return Manifest{}, fmt.Errorf("%s:%d: expected 3 tab-delimited fields: sample_id, annotation_json, genome_fasta", path, lineNo+1)
		}
		if !seenDataLine && isManifestHeader(fields) {
			seenDataLine = true
			continue
		}
		seenDataLine = true
		sampleID := fields[0]
		if sampleID == "" {
			return Manifest{}, fmt.Errorf("%s:%d: empty sample ID", path, lineNo+1)
		}
		if seen[sampleID] {
			return Manifest{}, fmt.Errorf("%s:%d: duplicate sample %q", path, lineNo+1, sampleID)
		}
		seen[sampleID] = true
		annotationPath := resolveManifestPath(base, fields[1])
		genomePath := resolveManifestPath(base, fields[2])
		manifest.Entries = append(manifest.Entries, ManifestEntry{
			SampleID:       sampleID,
			AnnotationPath: annotationPath,
			GenomePath:     genomePath,
		})
	}
	if len(manifest.Entries) == 0 {
		return Manifest{}, fmt.Errorf("%s: no manifest entries found", path)
	}
	return manifest, nil
}

func isManifestHeader(fields []string) bool {
	return isOneOfFold(fields[0], "sample", "sample_id", "sampleid") &&
		isOneOfFold(fields[1], "annotation", "annotation_json", "bakta_json") &&
		isOneOfFold(fields[2], "genome", "genome_fasta", "fasta")
}

func isOneOfFold(value string, choices ...string) bool {
	value = strings.ToLower(value)
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func OpenManifestSources(path string) (FileSource, FileSource, error) {
	manifest, err := ReadManifest(path)
	if err != nil {
		return nil, nil, err
	}
	return ManifestSource{Manifest: manifest, Role: "annotation"}, ManifestSource{Manifest: manifest, Role: "genome"}, nil
}

func resolveManifestPath(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

func splitTabFields(line string) []string {
	fields := strings.Split(line, "\t")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields
}

func OpenSource(path, kind, role string) (FileSource, error) {
	if kind == "" || kind == "auto" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		switch {
		case info.IsDir():
			kind = "dir"
		case isTarPath(path):
			kind = "tar"
		case strings.HasSuffix(path, ".agc"):
			kind = "agc"
		case role == "genome" && isGenomeFilename(filepath.Base(path)):
			kind = "file"
		default:
			kind = "list"
		}
	}
	switch kind {
	case "dir":
		return DirSource{Dir: path, Role: role}, nil
	case "file":
		if role != "genome" {
			return nil, fmt.Errorf("single file source is only supported for genomes")
		}
		sampleID := sampleIDFromName(filepath.Base(path), role)
		if sampleID == "" {
			return nil, fmt.Errorf("cannot infer sample ID from genome filename %q", path)
		}
		return SingleFileSource{File: sourceFile{
			SampleID: sampleID,
			Name:     filepath.Base(path),
			Path:     path,
		}}, nil
	case "list":
		return ListSource{Path: path, Role: role}, nil
	case "manifest":
		manifest, err := ReadManifest(path)
		if err != nil {
			return nil, err
		}
		return ManifestSource{Manifest: manifest, Role: role}, nil
	case "tar", "tar.gz", "tgz", "tar.bz2", "tbz2", "tbz", "tar.xz", "txz", "tar.zst", "tzst":
		compression := tarCompressionFromKind(kind)
		if kind == "tar" {
			compression = tarCompressionFromPath(path)
		}
		if compression == "xz" {
			return TarXZSource{Path: path, Role: role}, nil
		}
		return TarSource{Path: path, Role: role, Compression: compression}, nil
	case "agc":
		if role != "genome" {
			return nil, fmt.Errorf("agc source is only supported for genomes")
		}
		return AGCGenomeSource{Path: path, archive: &agcArchive{path: path}}, nil
	default:
		return nil, fmt.Errorf("unknown source kind %q", kind)
	}
}

type ManifestSource struct {
	Manifest Manifest
	Role     string
}

func (s ManifestSource) Records(ctx context.Context) ([]FileRecord, error) {
	records := make([]FileRecord, 0, len(s.Manifest.Entries))
	for _, entry := range s.Manifest.Entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		record, err := s.record(entry)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s ManifestSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	for _, entry := range s.Manifest.Entries {
		if entry.SampleID != sample {
			continue
		}
		select {
		case <-ctx.Done():
			return FileRecord{}, ctx.Err()
		default:
		}
		return s.record(entry)
	}
	return FileRecord{}, fmt.Errorf("sample %q not found", sample)
}

func (s ManifestSource) Order(ctx context.Context) ([]string, error) {
	order := make([]string, 0, len(s.Manifest.Entries))
	for _, entry := range s.Manifest.Entries {
		order = append(order, entry.SampleID)
	}
	return order, nil
}

func (s ManifestSource) record(entry ManifestEntry) (FileRecord, error) {
	path, err := s.path(entry)
	if err != nil {
		return FileRecord{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return FileRecord{}, err
	}
	return FileRecord{SampleID: entry.SampleID, Name: filepath.Base(path), Bytes: data}, nil
}

func (s ManifestSource) path(entry ManifestEntry) (string, error) {
	switch s.Role {
	case "annotation":
		return entry.AnnotationPath, nil
	case "genome":
		return entry.GenomePath, nil
	default:
		return "", fmt.Errorf("unknown manifest source role %q", s.Role)
	}
}

type DirSource struct {
	Dir  string
	Role string
}

func (s DirSource) Records(ctx context.Context) ([]FileRecord, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	return readSourceFileRecords(ctx, entries)
}

func (s DirSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	entries, err := s.entries()
	if err != nil {
		return FileRecord{}, err
	}
	entry, ok := findSourceFile(entries, sample)
	if !ok {
		return FileRecord{}, fmt.Errorf("sample %q not found", sample)
	}
	return readSourceFileRecord(ctx, entry)
}

func (s DirSource) Order(ctx context.Context) ([]string, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	return sourceFileOrder(entries), nil
}

func (s DirSource) entries() ([]sourceFile, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	files := make([]sourceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sampleID := sampleIDFromName(entry.Name(), s.Role)
		if sampleID == "" {
			continue
		}
		files = append(files, sourceFile{
			SampleID: sampleID,
			Name:     entry.Name(),
			Path:     filepath.Join(s.Dir, entry.Name()),
		})
	}
	sortSourceFiles(files)
	return files, nil
}

type ListSource struct {
	Path string
	Role string
}

func (s ListSource) Records(ctx context.Context) ([]FileRecord, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	return readSourceFileRecords(ctx, entries)
}

func (s ListSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	entries, err := s.entries()
	if err != nil {
		return FileRecord{}, err
	}
	entry, ok := findSourceFile(entries, sample)
	if !ok {
		return FileRecord{}, fmt.Errorf("sample %q not found", sample)
	}
	return readSourceFileRecord(ctx, entry)
}

func (s ListSource) Order(ctx context.Context) ([]string, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	return sourceFileOrder(entries), nil
}

type sourceFile struct {
	SampleID string
	Name     string
	Path     string
}

// SingleFileSource exposes one genome file as a source. Its sample ID is
// inferred from the file name, including recognised compression suffixes.
type SingleFileSource struct {
	File sourceFile
}

// WithSampleID returns a copy that exposes the genome as sampleID. It is used
// by the CLI when a single genome file accompanies an explicit sample argument.
func (s SingleFileSource) WithSampleID(sampleID string) SingleFileSource {
	s.File.SampleID = sampleID
	return s
}

func (s SingleFileSource) Records(ctx context.Context) ([]FileRecord, error) {
	record, err := readSourceFileRecord(ctx, s.File)
	if err != nil {
		return nil, err
	}
	return []FileRecord{record}, nil
}

func (s SingleFileSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	if sample != s.File.SampleID {
		return FileRecord{}, fmt.Errorf("sample %q not found", sample)
	}
	return readSourceFileRecord(ctx, s.File)
}

func (s SingleFileSource) Order(context.Context) ([]string, error) {
	return []string{s.File.SampleID}, nil
}

func readSourceFileRecords(ctx context.Context, entries []sourceFile) ([]FileRecord, error) {
	records := make([]FileRecord, 0, len(entries))
	for _, entry := range entries {
		record, err := readSourceFileRecord(ctx, entry)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func readSourceFileRecord(ctx context.Context, entry sourceFile) (FileRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return FileRecord{}, ctx.Err()
	default:
	}
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		return FileRecord{}, err
	}
	name := entry.Name
	if name == "" {
		name = filepath.Base(entry.Path)
	}
	return FileRecord{SampleID: entry.SampleID, Name: name, Bytes: data}, nil
}

func findSourceFile(entries []sourceFile, sample string) (sourceFile, bool) {
	for _, entry := range entries {
		if entry.SampleID == sample {
			return entry, true
		}
	}
	return sourceFile{}, false
}

func sourceFileOrder(entries []sourceFile) []string {
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		order = append(order, entry.SampleID)
	}
	return order
}

func sortSourceFiles(entries []sourceFile) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
}

func (s ListSource) entries() ([]sourceFile, error) {
	lines, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(s.Path)
	var entries []sourceFile
	for lineNo, raw := range strings.Split(string(lines), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var sampleID, path string
		if strings.Contains(line, "\t") {
			fields := splitTabFields(line)
			if len(fields) != 2 {
				return nil, fmt.Errorf("%s:%d: expected 2 tab-delimited fields: sample_id, path", s.Path, lineNo+1)
			}
			sampleID = fields[0]
			path = fields[1]
		} else {
			path = line
			sampleID = sampleIDFromName(filepath.Base(path), s.Role)
		}
		if sampleID == "" {
			return nil, fmt.Errorf("%s:%d: cannot infer sample ID", s.Path, lineNo+1)
		}
		if path == "" {
			return nil, fmt.Errorf("%s:%d: empty path", s.Path, lineNo+1)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		entries = append(entries, sourceFile{
			SampleID: sampleID,
			Name:     filepath.Base(path),
			Path:     path,
		})
	}
	return entries, nil
}

type TarXZSource struct {
	Path string
	Role string
}

func (s TarXZSource) Records(ctx context.Context) ([]FileRecord, error) {
	return s.tar().Records(ctx)
}

func (s TarXZSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	return s.tar().Get(ctx, sample)
}

func (s TarXZSource) Order(ctx context.Context) ([]string, error) {
	return s.tar().Order(ctx)
}

func (s TarXZSource) tar() tarSource {
	return tarSource{Path: s.Path, Role: s.Role, Compression: "xz"}
}

// TarSource reads members of a tar archive. Compression is "" for an
// uncompressed tar, or "gz", "bz2", "xz" or "zst".
type TarSource struct {
	Path        string
	Role        string
	Compression string
}

func (s TarSource) Records(ctx context.Context) ([]FileRecord, error) {
	return s.tar().Records(ctx)
}

func (s TarSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	return s.tar().Get(ctx, sample)
}

func (s TarSource) Order(ctx context.Context) ([]string, error) {
	return s.tar().Order(ctx)
}

func (s TarSource) tar() tarSource {
	return tarSource{Path: s.Path, Role: s.Role, Compression: s.Compression}
}

// tarSuffixes lists the file suffixes of each tar compression, uncompressed
// last so ".tar" does not shadow the longer suffixes.
var tarSuffixes = []struct {
	compression string
	suffixes    []string
}{
	{"gz", []string{".tar.gz", ".tgz"}},
	{"bz2", []string{".tar.bz2", ".tbz2", ".tbz"}},
	{"xz", []string{".tar.xz", ".txz"}},
	{"zst", []string{".tar.zst", ".tzst"}},
	{"", []string{".tar"}},
}

func isTarPath(path string) bool {
	_, ok := tarCompressionFromSuffix(path)
	return ok
}

// tarCompressionFromPath returns the compression a tar path's suffix implies,
// or "" for an uncompressed or unrecognised name.
func tarCompressionFromPath(path string) string {
	compression, _ := tarCompressionFromSuffix(path)
	return compression
}

func tarCompressionFromSuffix(path string) (string, bool) {
	lower := strings.ToLower(path)
	for _, entry := range tarSuffixes {
		for _, suffix := range entry.suffixes {
			if strings.HasSuffix(lower, suffix) {
				return entry.compression, true
			}
		}
	}
	return "", false
}

// tarCompressionFromKind maps a source format name such as "tar.gz" or
// "tgz" to its compression.
func tarCompressionFromKind(kind string) string {
	compression, _ := tarCompressionFromSuffix("." + kind)
	return compression
}

// tarSource is the shared implementation of TarSource and TarXZSource.
type tarSource struct {
	Path        string
	Role        string
	Compression string
}

func (s tarSource) Records(ctx context.Context) ([]FileRecord, error) {
	var records []FileRecord
	err := streamTarRecords(ctx, s, func(record FileRecord) error {
		records = append(records, record)
		return nil
	})
	return records, err
}

func (s tarSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	stream, err := newTarRecordStream(s)
	if err != nil {
		return FileRecord{}, err
	}
	defer stream.Close()
	for {
		record, ok, err := stream.Next(ctx)
		if err != nil {
			return FileRecord{}, err
		}
		if !ok {
			break
		}
		if record.SampleID == sample {
			return record, nil
		}
	}
	return FileRecord{}, fmt.Errorf("sample %q not found", sample)
}

func (s tarSource) Order(ctx context.Context) ([]string, error) {
	stream, err := newTarRecordStream(s)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var order []string
	for {
		sampleID, ok, err := stream.NextSampleID(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		order = append(order, sampleID)
	}
	return order, nil
}

func streamTarRecords(ctx context.Context, s tarSource, fn func(FileRecord) error) error {
	stream, err := newTarRecordStream(s)
	if err != nil {
		return err
	}
	defer stream.Close()
	for {
		record, ok, err := stream.Next(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := fn(record); err != nil {
			return err
		}
	}
}

type tarRecordStream struct {
	source       tarSource
	file         *os.File
	closeDecoder func()
	tr           *tar.Reader
}

// tarStreamOpened, if set, is called each time a tar source is opened, so
// tests can check how many times a build reads each tar.
var tarStreamOpened func(path string)

func newTarRecordStream(source tarSource) (*tarRecordStream, error) {
	if tarStreamOpened != nil {
		tarStreamOpened(source.Path)
	}
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	r, closeDecoder, err := tarDecoder(source, file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &tarRecordStream{source: source, file: file, closeDecoder: closeDecoder, tr: tar.NewReader(r)}, nil
}

// tarDecoder wraps file in the decompressor for source's compression. Each
// tar member is still read through maxDecompressedComponentSize, so a small
// compressed archive cannot expand into unbounded memory.
func tarDecoder(source tarSource, file *os.File) (io.Reader, func(), error) {
	switch source.Compression {
	case "":
		return file, func() {}, nil
	case "gz":
		r, err := gzip.NewReader(bufio.NewReader(file))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", source.Path, err)
		}
		return r, func() { r.Close() }, nil
	case "bz2":
		return bzip2.NewReader(bufio.NewReader(file)), func() {}, nil
	case "xz":
		r, err := xz.NewReader(bufio.NewReader(file))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", source.Path, err)
		}
		return r, func() {}, nil
	case "zst":
		r, err := zstd.NewReader(bufio.NewReader(file))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", source.Path, err)
		}
		return r, r.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported tar compression %q for %s", source.Compression, source.Path)
	}
}

func (s *tarRecordStream) Close() error {
	if s.file == nil {
		return nil
	}
	s.closeDecoder()
	return s.file.Close()
}

func (s *tarRecordStream) Next(ctx context.Context) (FileRecord, bool, error) {
	header, sampleID, ok, err := s.nextHeader(ctx)
	if err != nil || !ok {
		return FileRecord{}, ok, err
	}
	data, err := io.ReadAll(io.LimitReader(s.tr, maxDecompressedComponentSize+1))
	if err != nil {
		return FileRecord{}, false, err
	}
	if int64(len(data)) > maxDecompressedComponentSize {
		return FileRecord{}, false, fmt.Errorf("tar entry %q exceeds %d byte limit", header.Name, maxDecompressedComponentSize)
	}
	return FileRecord{SampleID: sampleID, Name: header.Name, Bytes: data}, true, nil
}

func (s *tarRecordStream) NextSampleID(ctx context.Context) (string, bool, error) {
	_, sampleID, ok, err := s.nextHeader(ctx)
	return sampleID, ok, err
}

func (s *tarRecordStream) nextHeader(ctx context.Context) (*tar.Header, string, bool, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, "", false, ctx.Err()
		default:
		}
		header, err := s.tr.Next()
		if err == io.EOF {
			return nil, "", false, nil
		}
		if err != nil {
			return nil, "", false, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		sampleID := sampleIDFromName(filepath.Base(header.Name), s.source.Role)
		if sampleID == "" {
			continue
		}
		return header, sampleID, true, nil
	}
}

// AGCGenomeSource reads genomes from an AGC archive with faqt's pure Go AGC
// reader, so no agc program is needed. Each sample is returned as FASTA text,
// named SAMPLE.fa.
type AGCGenomeSource struct {
	Path string
	// Command is ignored. Deprecated: AGC archives are read without the agc
	// program.
	Command string
	// Threads is ignored. Deprecated: AGC archives are read without the agc
	// program.
	Threads int

	// archive, when set by OpenSource, keeps the archive open across calls so
	// its index is read once. Without it each call opens the archive.
	archive *agcArchive
}

// agcArchive is an AGC archive opened on first use and shared by copies of an
// AGCGenomeSource. The lock serializes reads, as the archive is not documented
// as safe for concurrent use.
type agcArchive struct {
	mu      sync.Mutex
	path    string
	archive *agc.Archive
	err     error
}

func (s AGCGenomeSource) Records(ctx context.Context) ([]FileRecord, error) {
	order, err := s.Order(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]FileRecord, 0, len(order))
	for _, sample := range order {
		record, err := s.Get(ctx, sample)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (s AGCGenomeSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	var fasta []byte
	err := s.withArchive(func(archive *agc.Archive) error {
		reader, err := archive.OpenSample(sample)
		if err != nil {
			return err
		}
		fasta, err = agcSampleFASTA(ctx, reader)
		return err
	})
	if err != nil {
		return FileRecord{}, fmt.Errorf("AGC sample %s in %s: %w", sample, s.Path, err)
	}
	return FileRecord{SampleID: sample, Name: sample + ".fa", Bytes: fasta}, nil
}

func (s AGCGenomeSource) Order(ctx context.Context) ([]string, error) {
	var order []string
	err := s.withArchive(func(archive *agc.Archive) error {
		var err error
		order, err = archive.Samples()
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("AGC samples in %s: %w", s.Path, err)
	}
	return order, nil
}

// withArchive calls fn with the open archive, opening it if needed.
func (s AGCGenomeSource) withArchive(fn func(*agc.Archive) error) error {
	if s.archive == nil {
		archive, err := agc.OpenPath(s.Path)
		if err != nil {
			return err
		}
		defer archive.Close()
		return fn(archive)
	}
	s.archive.mu.Lock()
	defer s.archive.mu.Unlock()
	if s.archive.archive == nil && s.archive.err == nil {
		s.archive.archive, s.archive.err = agc.OpenPath(s.archive.path)
	}
	if s.archive.err != nil {
		return s.archive.err
	}
	return fn(s.archive.archive)
}

// agcSampleFASTA writes a sample's contigs as FASTA, one line per sequence,
// as agc getset does.
func agcSampleFASTA(ctx context.Context, reader *agc.Reader) ([]byte, error) {
	var buf bytes.Buffer
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := reader.Read()
		if err == io.EOF {
			return buf.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
		buf.WriteByte('>')
		buf.WriteString(record.Name)
		if record.Description != "" {
			buf.WriteByte(' ')
			buf.WriteString(record.Description)
		}
		buf.WriteByte('\n')
		buf.Write(record.Seq)
		buf.WriteByte('\n')
	}
}

// AnnotationJSONSuffixes lists the recognized Bakta annotation JSON filename
// suffixes, in the order checked. It's exported so cmd/bakpack's
// sampleIDFromPath (used by reduce/restore/gff3, which take an explicit JSON
// path rather than scanning a directory) can derive a sample ID from the
// same suffix list instead of maintaining its own separate copy. Reduced JSON
// names come first so that sampleA.reduced.bakta.json, as written by extract
// and reduce, pairs with sampleA's genome.
var AnnotationJSONSuffixes = []string{".reduced.bakta.json", ".reduced.json", ".bakta.json", ".json"}

func sampleIDFromName(name, role string) string {
	base := filepath.Base(name)
	suffixes := []string{}
	if role == "annotation" {
		suffixes = AnnotationJSONSuffixes
	} else {
		suffixes = []string{".fasta", ".fa", ".fna"}
	}
	for _, suffix := range compressionSuffixes {
		base = strings.TrimSuffix(base, suffix)
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return ""
}

var compressionSuffixes = []string{".gz", ".bz2", ".xz", ".zst"}

func isGenomeFilename(name string) bool {
	return sampleIDFromName(name, "genome") != ""
}
