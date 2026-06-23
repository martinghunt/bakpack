package bakpack

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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
		case strings.HasSuffix(path, ".tar.xz") || strings.HasSuffix(path, ".txz"):
			kind = "tar.xz"
		case strings.HasSuffix(path, ".agc"):
			kind = "agc"
		default:
			kind = "list"
		}
	}
	switch kind {
	case "dir":
		return DirSource{Dir: path, Role: role}, nil
	case "list":
		return ListSource{Path: path, Role: role}, nil
	case "manifest":
		manifest, err := ReadManifest(path)
		if err != nil {
			return nil, err
		}
		return ManifestSource{Manifest: manifest, Role: role}, nil
	case "tar.xz", "txz":
		return TarXZSource{Path: path, Role: role}, nil
	case "agc":
		if role != "genome" {
			return nil, fmt.Errorf("agc source is only supported for genomes")
		}
		return AGCGenomeSource{Path: path, Command: "agc"}, nil
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
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(s.Dir, entry.Name())
		if sampleIDFromName(entry.Name(), s.Role) == "" {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	records := make([]FileRecord, 0, len(paths))
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		records = append(records, FileRecord{SampleID: sampleIDFromName(name, s.Role), Name: name, Bytes: data})
	}
	return records, nil
}

func (s DirSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return FileRecord{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if sampleIDFromName(entry.Name(), s.Role) != sample {
			continue
		}
		path := filepath.Join(s.Dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return FileRecord{}, err
		}
		return FileRecord{SampleID: sample, Name: entry.Name(), Bytes: data}, nil
	}
	return FileRecord{}, fmt.Errorf("sample %q not found", sample)
}

func (s DirSource) Order(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if sampleIDFromName(entry.Name(), s.Role) == "" {
			continue
		}
		paths = append(paths, filepath.Join(s.Dir, entry.Name()))
	}
	sort.Strings(paths)
	order := make([]string, 0, len(paths))
	for _, path := range paths {
		order = append(order, sampleIDFromName(filepath.Base(path), s.Role))
	}
	return order, nil
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
	records := make([]FileRecord, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			return nil, err
		}
		records = append(records, FileRecord{SampleID: entry.SampleID, Name: filepath.Base(entry.Path), Bytes: data})
	}
	return records, nil
}

func (s ListSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	entries, err := s.entries()
	if err != nil {
		return FileRecord{}, err
	}
	for _, entry := range entries {
		if entry.SampleID != sample {
			continue
		}
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			return FileRecord{}, err
		}
		return FileRecord{SampleID: sample, Name: filepath.Base(entry.Path), Bytes: data}, nil
	}
	return FileRecord{}, fmt.Errorf("sample %q not found", sample)
}

func (s ListSource) Order(ctx context.Context) ([]string, error) {
	entries, err := s.entries()
	if err != nil {
		return nil, err
	}
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		order = append(order, entry.SampleID)
	}
	return order, nil
}

type listEntry struct {
	SampleID string
	Path     string
}

func (s ListSource) entries() ([]listEntry, error) {
	lines, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(s.Path)
	var entries []listEntry
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
		entries = append(entries, listEntry{SampleID: sampleID, Path: path})
	}
	return entries, nil
}

type TarXZSource struct {
	Path string
	Role string
}

func (s TarXZSource) Records(ctx context.Context) ([]FileRecord, error) {
	var records []FileRecord
	err := streamTarXZRecords(ctx, s, func(record FileRecord) error {
		records = append(records, record)
		return nil
	})
	return records, err
}

func (s TarXZSource) Get(ctx context.Context, sample string) (FileRecord, error) {
	stream, err := newTarXZRecordStream(s)
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

func (s TarXZSource) Order(ctx context.Context) ([]string, error) {
	stream, err := newTarXZRecordStream(s)
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

func streamTarXZRecords(ctx context.Context, s TarXZSource, fn func(FileRecord) error) error {
	stream, err := newTarXZRecordStream(s)
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

type tarXZRecordStream struct {
	source TarXZSource
	file   *os.File
	xzr    *xz.Reader
	tr     *tar.Reader
}

func newTarXZRecordStream(source TarXZSource) (*tarXZRecordStream, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	xzr, err := xz.NewReader(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	tr := tar.NewReader(xzr)
	return &tarXZRecordStream{source: source, file: file, xzr: xzr, tr: tr}, nil
}

func (s *tarXZRecordStream) Close() error {
	if s.file == nil {
		return nil
	}
	return s.file.Close()
}

func (s *tarXZRecordStream) Next(ctx context.Context) (FileRecord, bool, error) {
	for {
		select {
		case <-ctx.Done():
			return FileRecord{}, false, ctx.Err()
		default:
		}
		header, err := s.tr.Next()
		if err == io.EOF {
			return FileRecord{}, false, nil
		}
		if err != nil {
			return FileRecord{}, false, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		sampleID := sampleIDFromName(filepath.Base(header.Name), s.source.Role)
		if sampleID == "" {
			continue
		}
		data, err := io.ReadAll(s.tr)
		if err != nil {
			return FileRecord{}, false, err
		}
		return FileRecord{SampleID: sampleID, Name: header.Name, Bytes: data}, true, nil
	}
}

func (s *tarXZRecordStream) NextSampleID(ctx context.Context) (string, bool, error) {
	for {
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		default:
		}
		header, err := s.tr.Next()
		if err == io.EOF {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		sampleID := sampleIDFromName(filepath.Base(header.Name), s.source.Role)
		if sampleID == "" {
			continue
		}
		return sampleID, true, nil
	}
}

type AGCGenomeSource struct {
	Path    string
	Command string
	Threads int
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
	cmd := exec.CommandContext(ctx, s.commandName(), s.getsetArgs(sample)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return FileRecord{}, fmt.Errorf("agc getset %s: %w: %s", sample, err, strings.TrimSpace(stderr.String()))
	}
	return FileRecord{SampleID: sample, Name: sample + ".fa", Bytes: out}, nil
}

func (s AGCGenomeSource) Order(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, s.commandName(), "listset", s.Path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("agc listset: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var order []string
	for _, line := range strings.Split(string(out), "\n") {
		sample := strings.TrimSpace(line)
		if sample != "" {
			order = append(order, sample)
		}
	}
	return order, nil
}

func (s AGCGenomeSource) commandName() string {
	if s.Command == "" {
		return "agc"
	}
	return s.Command
}

func (s AGCGenomeSource) threadCount() int {
	if s.Threads <= 0 {
		return 1
	}
	return s.Threads
}

func (s AGCGenomeSource) getsetArgs(sample string) []string {
	return []string{"getset", "-t", strconv.Itoa(s.threadCount()), s.Path, sample}
}

func sampleIDFromName(name, role string) string {
	base := filepath.Base(name)
	suffixes := []string{}
	if role == "annotation" {
		suffixes = []string{".bakta.json", ".json"}
	} else {
		suffixes = []string{".fasta", ".fa", ".fna"}
	}
	for _, suffix := range suffixes {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix)
		}
	}
	return ""
}

func findRecord(records []FileRecord, sample string) (FileRecord, error) {
	for _, record := range records {
		if record.SampleID == sample {
			return record, nil
		}
	}
	return FileRecord{}, fmt.Errorf("sample %q not found", sample)
}

func recordOrder(records []FileRecord) []string {
	order := make([]string, 0, len(records))
	for _, record := range records {
		order = append(order, record.SampleID)
	}
	return order
}
