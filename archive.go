package bakpack

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ulikunitz/xz"
)

const (
	ArchiveMagic   = "BAKPACK1"
	ArchiveVersion = 1
)

type ArchiveIndex struct {
	Format        string        `json:"format"`
	Version       int           `json:"version"`
	PayloadFormat string        `json:"payload_format"`
	ChunkSize     int           `json:"chunk_size"`
	Chunks        []ChunkIndex  `json:"chunks"`
	Samples       []SampleIndex `json:"samples"`
}

type ChunkIndex struct {
	ID               int                `json:"id"`
	Offset           int64              `json:"offset"`
	CompressedSize   int64              `json:"compressed_size"`
	UncompressedSize int64              `json:"uncompressed_size"`
	TopKeys          []string           `json:"top_keys,omitempty"`
	ValueSchemas     []SchemaIndexEntry `json:"value_schemas,omitempty"`
	FeatureSchemas   []SchemaIndexEntry `json:"feature_schemas,omitempty"`
	FeatureFields    []string           `json:"feature_fields,omitempty"`
	FieldCodecs      []FieldCodec       `json:"field_codecs,omitempty"`
}

type SampleIndex struct {
	SampleID                    string `json:"sample_id"`
	AnnotationName              string `json:"annotation_name"`
	GenomeName                  string `json:"genome_name"`
	ChunkID                     int    `json:"chunk_id"`
	OriginalJSONCanonicalSHA256 string `json:"original_json_canonical_sha256"`
	ReducedJSONCanonicalSHA256  string `json:"reduced_json_canonical_sha256"`
}

type BuildOptions struct {
	Annotations FileSource
	Genomes     FileSource
	Order       []string
	ChunkSize   int
	OutputPath  string
	XZThreads   int

	// AnnotationSpoolCompression controls temporary files made when building
	// from annotation tar.xz sources whose order differs from genome order.
	// Supported values are "", "gzip", "none", and "raw"; "" defaults to gzip.
	AnnotationSpoolCompression string
}

type ExtractOptions struct {
	ArchivePath        string
	Genomes            FileSource
	Samples            []string
	OutputDir          string
	Reduced            bool
	Original           bool
	Genome             bool
	GFF3               bool
	GFF3AnnotationOnly bool
}

// OpenArchiveOptions configures archive reads.
type OpenArchiveOptions struct {
	// HTTPClient is used for byte-range requests when the archive path is an
	// HTTP(S) URL. A nil client uses http.DefaultClient.
	HTTPClient *http.Client
}

// Archive is an opened bakpack archive. It keeps the archive index in memory
// and can extract one or more samples without reopening the archive.
type Archive struct {
	reader      archiveRangeReader
	index       ArchiveIndex
	chunkStart  int64
	sampleIndex map[string]SampleIndex
	chunkIndex  map[int]ChunkIndex
}

// ExtractRequest configures extraction from an opened archive.
type ExtractRequest struct {
	// Genomes is required when Original, Genome, or GFF3 output is requested.
	Genomes FileSource
	// Samples are the sample IDs to extract.
	Samples []string
	// Reduced returns reduced Bakta JSON.
	Reduced bool
	// Original reconstructs original Bakta JSON and verifies its canonical hash.
	Original bool
	// Genome returns matching genome FASTA.
	Genome bool
	// GFF3 renders a Bakta-style GFF3 annotation.
	GFF3 bool
	// GFF3AnnotationOnly returns GFF3 output without the terminal ##FASTA section.
	// It implies GFF3.
	GFF3AnnotationOnly bool
	// OnSample is called for each extracted sample. When nil, Extract returns
	// accumulated results in the same order as Samples.
	OnSample func(ExtractedSample) error
}

// ExtractedSample contains the bytes extracted or reconstructed for one sample.
type ExtractedSample struct {
	SampleID                    string
	AnnotationName              string
	GenomeName                  string
	ReducedJSON                 []byte
	OriginalJSON                []byte
	GenomeFASTA                 []byte
	GFF3                        []byte
	OriginalJSONCanonicalSHA256 string
	ReducedJSONCanonicalSHA256  string
}

func BuildArchive(ctx context.Context, opts BuildOptions) error {
	if opts.Annotations == nil {
		return fmt.Errorf("annotation source is required")
	}
	if opts.Genomes == nil {
		return fmt.Errorf("genome source is required")
	}
	if opts.OutputPath == "" {
		return fmt.Errorf("output path is required")
	}
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 25
	}
	annotationsTar, annotationsOK := asTarXZSource(opts.Annotations)
	genomesTar, genomesOK := asTarXZSource(opts.Genomes)
	if len(opts.Order) == 0 {
		if annotationsOK && genomesOK {
			sameOrder, err := tarXZSourcesHaveSameOrder(ctx, annotationsTar, genomesTar)
			if err != nil {
				return err
			}
			if sameOrder {
				return buildArchiveFromPairedTarXZ(ctx, opts, annotationsTar, genomesTar, chunkSize)
			}
		}
	}
	if annotationsOK {
		return buildArchiveFromSpooledAnnotationTar(ctx, opts, annotationsTar, chunkSize)
	}

	return buildArchiveFromIndexedSources(ctx, opts, chunkSize)
}

func buildArchiveMaterialized(ctx context.Context, opts BuildOptions, chunkSize int) error {
	annotationRecords, err := opts.Annotations.Records(ctx)
	if err != nil {
		return err
	}
	annotations, err := recordsBySample(annotationRecords)
	if err != nil {
		return err
	}
	genomeRecords, err := opts.Genomes.Records(ctx)
	if err != nil {
		return err
	}
	genomes, err := recordsBySample(genomeRecords)
	if err != nil {
		return err
	}
	order, err := buildOrder(ctx, opts, annotations)
	if err != nil {
		return err
	}

	var packed []packedSampleForArchive
	for _, sample := range order {
		annotation, ok := annotations[sample]
		if !ok {
			return fmt.Errorf("annotation for sample %q not found", sample)
		}
		genomeRecord, ok := genomes[sample]
		if !ok {
			return fmt.Errorf("genome for sample %q not found", sample)
		}
		packedSample, err := packReducedSample(sample, annotation, genomeRecord)
		if err != nil {
			return err
		}
		packed = append(packed, packedSample)
	}

	chunks, samples, chunkPayloads, err := makeArchiveChunks(packed, chunkSize, opts)
	if err != nil {
		return err
	}
	index := ArchiveIndex{
		Format:        "bakpack",
		Version:       ArchiveVersion,
		PayloadFormat: optimizedPayloadFormat,
		ChunkSize:     chunkSize,
		Chunks:        chunks,
		Samples:       samples,
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	indexBytes, err = xzCompress(indexBytes, opts)
	if err != nil {
		return err
	}

	out, err := os.Create(opts.OutputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write([]byte(ArchiveMagic)); err != nil {
		return err
	}
	if err := binary.Write(out, binary.LittleEndian, uint64(len(indexBytes))); err != nil {
		return err
	}
	if _, err := out.Write(indexBytes); err != nil {
		return err
	}
	for _, payload := range chunkPayloads {
		if _, err := out.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func buildArchiveFromIndexedSources(ctx context.Context, opts BuildOptions, chunkSize int) error {
	order, err := buildOrderFromSources(ctx, opts)
	if err != nil {
		return err
	}

	chunkFile, err := os.CreateTemp(filepath.Dir(opts.OutputPath), ".bakpack-chunks-*")
	if err != nil {
		return err
	}
	defer os.Remove(chunkFile.Name())
	defer chunkFile.Close()

	chunks, samples, err := makeArchiveChunksFromIndexedSources(ctx, opts, order, chunkSize, chunkFile)
	if err != nil {
		return err
	}
	if _, err := chunkFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	index := ArchiveIndex{
		Format:        "bakpack",
		Version:       ArchiveVersion,
		PayloadFormat: optimizedPayloadFormat,
		ChunkSize:     chunkSize,
		Chunks:        chunks,
		Samples:       samples,
	}
	return writeArchiveFile(opts.OutputPath, index, opts, chunkFile)
}

func makeArchiveChunksFromIndexedSources(ctx context.Context, opts BuildOptions, order []string, chunkSize int, chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
	var chunks []ChunkIndex
	var samples []SampleIndex
	var batch []packedSampleForArchive
	var relativeOffset int64
	chunkID := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		chunk, sampleIndexes, compressed, err := encodeArchiveChunk(chunkID, batch, opts)
		if err != nil {
			return err
		}
		written, err := chunkWriter.Write(compressed)
		if err != nil {
			return err
		}
		if written != len(compressed) {
			return io.ErrShortWrite
		}
		chunk.Offset = relativeOffset
		chunks = append(chunks, chunk)
		relativeOffset += int64(len(compressed))
		samples = append(samples, sampleIndexes...)
		for i := range batch {
			batch[i].reduced = nil
			batch[i].reducedRoot = nil
		}
		batch = batch[:0]
		chunkID++
		return nil
	}

	for _, sample := range order {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		packed, err := packSampleFromSources(ctx, opts, sample)
		if err != nil {
			return nil, nil, err
		}
		batch = append(batch, packed)
		if len(batch) == chunkSize {
			if err := flush(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return chunks, samples, nil
}

func buildArchiveFromPairedTarXZ(ctx context.Context, opts BuildOptions, annotationsTar, genomesTar TarXZSource, chunkSize int) error {
	chunkFile, err := os.CreateTemp(filepath.Dir(opts.OutputPath), ".bakpack-chunks-*")
	if err != nil {
		return err
	}
	defer os.Remove(chunkFile.Name())
	defer chunkFile.Close()

	chunks, samples, err := makeArchiveChunksFromPairedTarXZ(ctx, opts, annotationsTar, genomesTar, chunkSize, chunkFile)
	if err != nil {
		return err
	}
	if _, err := chunkFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	index := ArchiveIndex{
		Format:        "bakpack",
		Version:       ArchiveVersion,
		PayloadFormat: optimizedPayloadFormat,
		ChunkSize:     chunkSize,
		Chunks:        chunks,
		Samples:       samples,
	}
	return writeArchiveFile(opts.OutputPath, index, opts, chunkFile)
}

func makeArchiveChunksFromPairedTarXZ(ctx context.Context, opts BuildOptions, annotationsTar, genomesTar TarXZSource, chunkSize int, chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
	var chunks []ChunkIndex
	var samples []SampleIndex
	var batch []packedSampleForArchive
	var relativeOffset int64
	chunkID := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		chunk, sampleIndexes, compressed, err := encodeArchiveChunk(chunkID, batch, opts)
		if err != nil {
			return err
		}
		written, err := chunkWriter.Write(compressed)
		if err != nil {
			return err
		}
		if written != len(compressed) {
			return io.ErrShortWrite
		}
		chunk.Offset = relativeOffset
		chunks = append(chunks, chunk)
		relativeOffset += int64(len(compressed))
		samples = append(samples, sampleIndexes...)
		for i := range batch {
			batch[i].reduced = nil
			batch[i].reducedRoot = nil
		}
		batch = batch[:0]
		chunkID++
		return nil
	}

	if err := streamPairedTarXZRecords(ctx, annotationsTar, genomesTar, func(annotation, genomeRecord FileRecord) error {
		packed, err := packReducedSample(annotation.SampleID, annotation, genomeRecord)
		if err != nil {
			return err
		}
		batch = append(batch, packed)
		if len(batch) == chunkSize {
			return flush()
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return chunks, samples, nil
}

type spooledAnnotation struct {
	SampleID string
	Name     string
	Path     string
}

func buildArchiveFromSpooledAnnotationTar(ctx context.Context, opts BuildOptions, annotationsTar TarXZSource, chunkSize int) error {
	spoolCompression, err := normalizeSpoolCompression(opts.AnnotationSpoolCompression)
	if err != nil {
		return err
	}
	spoolDir, err := os.MkdirTemp(filepath.Dir(opts.OutputPath), ".bakpack-annotations-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(spoolDir)

	annotations, err := spoolAnnotationTar(ctx, annotationsTar, spoolDir, spoolCompression)
	if err != nil {
		return err
	}
	order, err := buildOrderFromSpooledAnnotations(ctx, opts, annotations)
	if err != nil {
		return err
	}

	chunkFile, err := os.CreateTemp(filepath.Dir(opts.OutputPath), ".bakpack-chunks-*")
	if err != nil {
		return err
	}
	defer os.Remove(chunkFile.Name())
	defer chunkFile.Close()

	chunks, samples, err := makeArchiveChunksFromSpooledAnnotations(ctx, opts, annotations, order, chunkSize, chunkFile)
	if err != nil {
		return err
	}
	if _, err := chunkFile.Seek(0, io.SeekStart); err != nil {
		return err
	}

	index := ArchiveIndex{
		Format:        "bakpack",
		Version:       ArchiveVersion,
		PayloadFormat: optimizedPayloadFormat,
		ChunkSize:     chunkSize,
		Chunks:        chunks,
		Samples:       samples,
	}
	return writeArchiveFile(opts.OutputPath, index, opts, chunkFile)
}

func spoolAnnotationTar(ctx context.Context, annotationsTar TarXZSource, spoolDir, spoolCompression string) (map[string]spooledAnnotation, error) {
	annotations := map[string]spooledAnnotation{}
	count := 0
	err := streamTarXZRecords(ctx, annotationsTar, func(record FileRecord) error {
		if _, exists := annotations[record.SampleID]; exists {
			return fmt.Errorf("duplicate annotation sample %q", record.SampleID)
		}
		path := filepath.Join(spoolDir, fmt.Sprintf("%06d%s", count, spoolFileSuffix(spoolCompression)))
		count++
		if err := writeSpoolFile(path, record.Bytes, spoolCompression); err != nil {
			return err
		}
		annotations[record.SampleID] = spooledAnnotation{
			SampleID: record.SampleID,
			Name:     record.Name,
			Path:     path,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(annotations) == 0 {
		return nil, fmt.Errorf("no annotation JSON files found in %s", annotationsTar.Path)
	}
	return annotations, nil
}

func buildOrderFromSpooledAnnotations(ctx context.Context, opts BuildOptions, annotations map[string]spooledAnnotation) ([]string, error) {
	annotationRecords := make(map[string]FileRecord, len(annotations))
	for sample, annotation := range annotations {
		annotationRecords[sample] = FileRecord{SampleID: sample, Name: annotation.Name}
	}
	return buildOrder(ctx, opts, annotationRecords)
}

func makeArchiveChunksFromSpooledAnnotations(ctx context.Context, opts BuildOptions, annotations map[string]spooledAnnotation, order []string, chunkSize int, chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
	var chunks []ChunkIndex
	var samples []SampleIndex
	var batch []packedSampleForArchive
	var relativeOffset int64
	chunkID := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		chunk, sampleIndexes, compressed, err := encodeArchiveChunk(chunkID, batch, opts)
		if err != nil {
			return err
		}
		written, err := chunkWriter.Write(compressed)
		if err != nil {
			return err
		}
		if written != len(compressed) {
			return io.ErrShortWrite
		}
		chunk.Offset = relativeOffset
		chunks = append(chunks, chunk)
		relativeOffset += int64(len(compressed))
		samples = append(samples, sampleIndexes...)
		for i := range batch {
			batch[i].reduced = nil
			batch[i].reducedRoot = nil
		}
		batch = batch[:0]
		chunkID++
		return nil
	}

	err := forEachSpooledAnnotationSample(ctx, opts, annotations, order, func(packed packedSampleForArchive) error {
		batch = append(batch, packed)
		if annotation, ok := annotations[packed.index.SampleID]; ok {
			_ = os.Remove(annotation.Path)
		}
		if len(batch) == chunkSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if err := flush(); err != nil {
		return nil, nil, err
	}
	return chunks, samples, nil
}

func forEachSpooledAnnotationSample(ctx context.Context, opts BuildOptions, annotations map[string]spooledAnnotation, order []string, fn func(packedSampleForArchive) error) error {
	if genomesTar, ok := asTarXZSource(opts.Genomes); ok && len(opts.Order) == 0 {
		wanted := map[string]bool{}
		for _, sample := range order {
			wanted[sample] = true
		}
		seen := 0
		err := streamTarXZRecords(ctx, genomesTar, func(genomeRecord FileRecord) error {
			if !wanted[genomeRecord.SampleID] {
				return nil
			}
			annotation, err := loadSpooledAnnotation(annotations[genomeRecord.SampleID])
			if err != nil {
				return err
			}
			packed, err := packReducedSample(genomeRecord.SampleID, annotation, genomeRecord)
			if err != nil {
				return err
			}
			seen++
			return fn(packed)
		})
		if err != nil {
			return err
		}
		if seen != len(order) {
			return fmt.Errorf("genome source did not include all annotation samples")
		}
		return nil
	}

	for _, sample := range order {
		annotation, ok := annotations[sample]
		if !ok {
			return fmt.Errorf("annotation for sample %q not found", sample)
		}
		annotationRecord, err := loadSpooledAnnotation(annotation)
		if err != nil {
			return err
		}
		genomeRecord, err := opts.Genomes.Get(ctx, sample)
		if err != nil {
			return err
		}
		packed, err := packReducedSample(sample, annotationRecord, genomeRecord)
		if err != nil {
			return err
		}
		if err := fn(packed); err != nil {
			return err
		}
	}
	return nil
}

func loadSpooledAnnotation(annotation spooledAnnotation) (FileRecord, error) {
	data, err := readSpoolFile(annotation.Path)
	if err != nil {
		return FileRecord{}, err
	}
	return FileRecord{SampleID: annotation.SampleID, Name: annotation.Name, Bytes: data}, nil
}

func normalizeSpoolCompression(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "gzip", "gz":
		return "gzip", nil
	case "none", "raw":
		return "none", nil
	default:
		return "", fmt.Errorf("unknown annotation spool compression %q", value)
	}
}

func spoolFileSuffix(compression string) string {
	if compression == "gzip" {
		return ".json.gz"
	}
	return ".json"
}

func writeSpoolFile(path string, data []byte, compression string) error {
	if compression != "gzip" {
		return os.WriteFile(path, data, 0o600)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	writer, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return err
	}
	return writer.Close()
}

func readSpoolFile(path string) ([]byte, error) {
	if !strings.HasSuffix(path, ".gz") {
		return os.ReadFile(path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func streamPairedTarXZRecords(ctx context.Context, annotationsTar, genomesTar TarXZSource, fn func(annotation, genome FileRecord) error) error {
	annotationStream, err := newTarXZRecordStream(annotationsTar)
	if err != nil {
		return err
	}
	defer annotationStream.Close()
	genomeStream, err := newTarXZRecordStream(genomesTar)
	if err != nil {
		return err
	}
	defer genomeStream.Close()

	for {
		annotation, annotationOK, err := annotationStream.Next(ctx)
		if err != nil {
			return err
		}
		genome, genomeOK, err := genomeStream.Next(ctx)
		if err != nil {
			return err
		}
		if !annotationOK && !genomeOK {
			return nil
		}
		if annotationOK != genomeOK {
			return fmt.Errorf("annotation and genome tar.xz sources have different sample counts")
		}
		if annotation.SampleID != genome.SampleID {
			return fmt.Errorf("annotation sample %q does not match genome sample %q in tar.xz stream", annotation.SampleID, genome.SampleID)
		}
		if err := fn(annotation, genome); err != nil {
			return err
		}
	}
}

func packReducedSample(sample string, annotation, genomeRecord FileRecord) (packedSampleForArchive, error) {
	genome, err := ReadGenome(sample, genomeRecord.Name, genomeRecord.Bytes)
	if err != nil {
		return packedSampleForArchive{}, fmt.Errorf("%s: %w", sample, err)
	}
	reduced, err := ReduceBaktaJSON(annotation.Bytes, genome)
	if err != nil {
		return packedSampleForArchive{}, fmt.Errorf("%s: reduce Bakta JSON: %w", sample, err)
	}
	return packedSampleForArchive{
		index: SampleIndex{
			SampleID:                    sample,
			AnnotationName:              annotation.Name,
			GenomeName:                  genomeRecord.Name,
			OriginalJSONCanonicalSHA256: reduced.Original.CanonicalSHA256,
			ReducedJSONCanonicalSHA256:  reduced.Reduced.CanonicalSHA256,
		},
		reduced: reduced.ReducedJSON,
	}, nil
}

func asTarXZSource(source FileSource) (TarXZSource, bool) {
	switch source := source.(type) {
	case TarXZSource:
		return source, true
	case *TarXZSource:
		return *source, true
	default:
		return TarXZSource{}, false
	}
}

func tarXZSourcesHaveSameOrder(ctx context.Context, annotationsTar, genomesTar TarXZSource) (bool, error) {
	annotationStream, err := newTarXZRecordStream(annotationsTar)
	if err != nil {
		return false, err
	}
	defer annotationStream.Close()
	genomeStream, err := newTarXZRecordStream(genomesTar)
	if err != nil {
		return false, err
	}
	defer genomeStream.Close()

	for {
		annotationSample, annotationOK, err := annotationStream.NextSampleID(ctx)
		if err != nil {
			return false, err
		}
		genomeSample, genomeOK, err := genomeStream.NextSampleID(ctx)
		if err != nil {
			return false, err
		}
		if !annotationOK && !genomeOK {
			return true, nil
		}
		if annotationOK != genomeOK {
			return false, nil
		}
		if annotationSample != genomeSample {
			return false, nil
		}
	}
}

func writeArchiveFile(path string, index ArchiveIndex, opts BuildOptions, chunks io.Reader) error {
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	indexBytes, err = xzCompress(indexBytes, opts)
	if err != nil {
		return err
	}

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write([]byte(ArchiveMagic)); err != nil {
		return err
	}
	if err := binary.Write(out, binary.LittleEndian, uint64(len(indexBytes))); err != nil {
		return err
	}
	if _, err := out.Write(indexBytes); err != nil {
		return err
	}
	_, err = io.Copy(out, chunks)
	return err
}

func getGenomeRecords(ctx context.Context, source FileSource, samples []string) (map[string]FileRecord, error) {
	wanted := map[string]bool{}
	for _, sample := range samples {
		wanted[sample] = true
	}
	records := map[string]FileRecord{}
	if genomesTar, ok := asTarXZSource(source); ok {
		err := streamTarXZRecords(ctx, genomesTar, func(record FileRecord) error {
			if !wanted[record.SampleID] {
				return nil
			}
			records[record.SampleID] = record
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		for _, sample := range samples {
			record, err := source.Get(ctx, sample)
			if err != nil {
				return nil, err
			}
			records[sample] = record
		}
	}
	for _, sample := range samples {
		if _, ok := records[sample]; !ok {
			return nil, fmt.Errorf("genome for sample %q not found", sample)
		}
	}
	return records, nil
}

type genomeRecordProvider interface {
	Get(context.Context, string) (FileRecord, error)
}

type mapGenomeRecordProvider map[string]FileRecord

func (p mapGenomeRecordProvider) Get(_ context.Context, sample string) (FileRecord, error) {
	record, ok := p[sample]
	if !ok {
		return FileRecord{}, fmt.Errorf("genome for sample %q not found", sample)
	}
	return record, nil
}

type sourceGenomeRecordProvider struct {
	source FileSource
}

func (p sourceGenomeRecordProvider) Get(ctx context.Context, sample string) (FileRecord, error) {
	record, err := p.source.Get(ctx, sample)
	if err != nil {
		return FileRecord{}, err
	}
	if record.SampleID == "" {
		record.SampleID = sample
	}
	return record, nil
}

func newGenomeRecordProvider(ctx context.Context, source FileSource, samples []string) (genomeRecordProvider, error) {
	if genomesTar, ok := asTarXZSource(source); ok {
		records, err := getGenomeRecords(ctx, genomesTar, samples)
		if err != nil {
			return nil, err
		}
		return mapGenomeRecordProvider(records), nil
	}
	return sourceGenomeRecordProvider{source: source}, nil
}

// OpenArchive opens a local or HTTP(S) bakpack archive and reads its front
// index. HTTP(S) archives are accessed with byte-range requests.
func OpenArchive(ctx context.Context, path string, opts ...OpenArchiveOptions) (*Archive, error) {
	openOpts, err := mergeOpenArchiveOptions(opts)
	if err != nil {
		return nil, err
	}
	reader, index, chunkStart, err := openArchive(ctx, path, openOpts)
	if err != nil {
		return nil, err
	}
	archive := &Archive{
		reader:      reader,
		index:       index,
		chunkStart:  chunkStart,
		sampleIndex: map[string]SampleIndex{},
		chunkIndex:  map[int]ChunkIndex{},
	}
	for _, sample := range index.Samples {
		archive.sampleIndex[sample.SampleID] = sample
	}
	for _, chunk := range index.Chunks {
		archive.chunkIndex[chunk.ID] = chunk
	}
	return archive, nil
}

// Close closes the underlying archive reader.
func (a *Archive) Close() error {
	if a == nil || a.reader == nil {
		return nil
	}
	err := a.reader.Close()
	a.reader = nil
	return err
}

// Index returns a copy of the archive index.
func (a *Archive) Index() ArchiveIndex {
	if a == nil {
		return ArchiveIndex{}
	}
	return cloneArchiveIndex(a.index)
}

// SampleIDs returns sample IDs in archive order.
func (a *Archive) SampleIDs() []string {
	if a == nil {
		return nil
	}
	samples := make([]string, 0, len(a.index.Samples))
	for _, sample := range a.index.Samples {
		samples = append(samples, sample.SampleID)
	}
	return samples
}

// Extract extracts one or more samples from the archive. Without OnSample,
// results are returned in the same order as req.Samples. With OnSample, results
// are delivered to the callback and the returned slice is nil.
func (a *Archive) Extract(ctx context.Context, req ExtractRequest) ([]ExtractedSample, error) {
	if a == nil || a.reader == nil {
		return nil, fmt.Errorf("archive is closed or nil")
	}
	if req.GFF3AnnotationOnly {
		req.GFF3 = true
	}
	if !req.Reduced && !req.Original && !req.Genome && !req.GFF3 {
		req.Reduced = true
	}
	if len(req.Samples) == 0 {
		return nil, nil
	}
	if (req.Original || req.Genome || req.GFF3) && req.Genomes == nil {
		return nil, fmt.Errorf("genome source is required for original JSON, FASTA, or GFF3 extraction")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	samplesByChunk := map[int][]string{}
	chunkOrderSet := map[int]bool{}
	var chunkOrder []int
	for _, sample := range req.Samples {
		entry, ok := a.sampleIndex[sample]
		if !ok {
			return nil, fmt.Errorf("sample %q not found in archive", sample)
		}
		samplesByChunk[entry.ChunkID] = append(samplesByChunk[entry.ChunkID], sample)
		if !chunkOrderSet[entry.ChunkID] {
			chunkOrderSet[entry.ChunkID] = true
			chunkOrder = append(chunkOrder, entry.ChunkID)
		}
	}
	sort.Ints(chunkOrder)

	var genomes genomeRecordProvider
	var err error
	if req.Original || req.Genome || req.GFF3 {
		genomes, err = newGenomeRecordProvider(ctx, req.Genomes, req.Samples)
		if err != nil {
			return nil, err
		}
	}

	resultsBySample := map[string]ExtractedSample{}
	for _, chunkID := range chunkOrder {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		chunk, ok := a.chunkIndex[chunkID]
		if !ok {
			return nil, fmt.Errorf("chunk %d missing from index", chunkID)
		}
		samples := samplesByChunk[chunkID]
		reducedBySample, err := readChunk(ctx, a.reader, a.chunkStart, chunk, a.index, samples)
		if err != nil {
			return nil, err
		}
		for _, sample := range samples {
			entry := a.sampleIndex[sample]
			reducedJSON, ok := reducedBySample[sample]
			if !ok {
				return nil, fmt.Errorf("sample %q missing from chunk %d", sample, chunkID)
			}
			if err := verifyReduced(entry, reducedJSON); err != nil {
				return nil, err
			}
			if err := validateBaktaJSONFeatureTypes(reducedJSON); err != nil {
				return nil, fmt.Errorf("%s: %w", sample, err)
			}
			result := ExtractedSample{
				SampleID:                    sample,
				AnnotationName:              entry.AnnotationName,
				GenomeName:                  entry.GenomeName,
				OriginalJSONCanonicalSHA256: entry.OriginalJSONCanonicalSHA256,
				ReducedJSONCanonicalSHA256:  entry.ReducedJSONCanonicalSHA256,
			}
			if req.Reduced {
				result.ReducedJSON = append([]byte(nil), reducedJSON...)
			}
			var genome Genome
			if req.Original || req.Genome || req.GFF3 {
				record, err := genomes.Get(ctx, sample)
				if err != nil {
					return nil, err
				}
				genome, err = ReadGenome(sample, record.Name, record.Bytes)
				if err != nil {
					return nil, err
				}
				if req.Genome {
					result.GenomeFASTA = genome.FASTABytes(80)
				}
			}
			if req.GFF3 {
				gff3, err := BaktaGFF3WithOptions(reducedJSON, genome, BaktaGFF3Options{
					AnnotationOnly: req.GFF3AnnotationOnly,
				})
				if err != nil {
					return nil, err
				}
				result.GFF3 = gff3
			}
			if req.Original {
				restored, err := RestoreBaktaJSON(reducedJSON, genome)
				if err != nil {
					return nil, err
				}
				if restored.Original.CanonicalSHA256 != entry.OriginalJSONCanonicalSHA256 {
					return nil, fmt.Errorf("sample %s original canonical SHA-256 mismatch", sample)
				}
				result.OriginalJSON = restored.OriginalJSON
			}
			if req.OnSample != nil {
				if err := req.OnSample(result); err != nil {
					return nil, err
				}
			} else {
				resultsBySample[sample] = result
			}
		}
	}
	if req.OnSample != nil {
		return nil, nil
	}
	results := make([]ExtractedSample, 0, len(req.Samples))
	for _, sample := range req.Samples {
		results = append(results, resultsBySample[sample])
	}
	return results, nil
}

func ExtractArchive(ctx context.Context, opts ExtractOptions) error {
	if opts.GFF3AnnotationOnly {
		opts.GFF3 = true
	}
	if !opts.Reduced && !opts.Original && !opts.Genome && !opts.GFF3 {
		opts.Reduced = true
	}
	if len(opts.Samples) == 0 {
		return nil
	}
	if (opts.Original || opts.Genome || opts.GFF3) && opts.Genomes == nil {
		return fmt.Errorf("genome source is required for original JSON, FASTA, or GFF3 extraction")
	}
	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = "."
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}

	archive, err := OpenArchive(ctx, opts.ArchivePath)
	if err != nil {
		return err
	}
	defer archive.Close()

	_, err = archive.Extract(ctx, ExtractRequest{
		Genomes:            opts.Genomes,
		Samples:            opts.Samples,
		Reduced:            opts.Reduced,
		Original:           opts.Original,
		Genome:             opts.Genome,
		GFF3:               opts.GFF3,
		GFF3AnnotationOnly: opts.GFF3AnnotationOnly,
		OnSample: func(sample ExtractedSample) error {
			if opts.Genome {
				if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".fa"), sample.GenomeFASTA, 0o644); err != nil {
					return err
				}
			}
			if opts.Reduced {
				if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".reduced.bakta.json"), sample.ReducedJSON, 0o644); err != nil {
					return err
				}
			}
			if opts.Original {
				if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".bakta.json"), sample.OriginalJSON, 0o644); err != nil {
					return err
				}
			}
			if opts.GFF3 {
				if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".gff3"), sample.GFF3, 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	})
	return err
}

func ReadArchiveIndex(path string) (ArchiveIndex, error) {
	return ReadArchiveIndexContext(context.Background(), path)
}

// ReadArchiveIndexContext reads the front index from a local or HTTP(S)
// archive. HTTP(S) archives are accessed with byte-range requests.
func ReadArchiveIndexContext(ctx context.Context, path string, opts ...OpenArchiveOptions) (ArchiveIndex, error) {
	archive, err := OpenArchive(ctx, path, opts...)
	if err != nil {
		return ArchiveIndex{}, err
	}
	defer archive.Close()
	return archive.Index(), nil
}

func mergeOpenArchiveOptions(opts []OpenArchiveOptions) (OpenArchiveOptions, error) {
	switch len(opts) {
	case 0:
		return OpenArchiveOptions{}, nil
	case 1:
		return opts[0], nil
	default:
		return OpenArchiveOptions{}, fmt.Errorf("expected at most one OpenArchiveOptions value")
	}
}

func cloneArchiveIndex(index ArchiveIndex) ArchiveIndex {
	out := index
	out.Chunks = append([]ChunkIndex(nil), index.Chunks...)
	for i := range out.Chunks {
		out.Chunks[i].TopKeys = append([]string(nil), out.Chunks[i].TopKeys...)
		out.Chunks[i].FeatureFields = append([]string(nil), out.Chunks[i].FeatureFields...)
		out.Chunks[i].ValueSchemas = cloneSchemaIndexEntries(out.Chunks[i].ValueSchemas)
		out.Chunks[i].FeatureSchemas = cloneSchemaIndexEntries(out.Chunks[i].FeatureSchemas)
		out.Chunks[i].FieldCodecs = cloneFieldCodecs(out.Chunks[i].FieldCodecs)
	}
	out.Samples = append([]SampleIndex(nil), index.Samples...)
	return out
}

func cloneSchemaIndexEntries(entries []SchemaIndexEntry) []SchemaIndexEntry {
	out := append([]SchemaIndexEntry(nil), entries...)
	for i := range out {
		out[i].Keys = append([]string(nil), out[i].Keys...)
	}
	return out
}

func cloneFieldCodecs(codecs []FieldCodec) []FieldCodec {
	out := append([]FieldCodec(nil), codecs...)
	for i := range out {
		out[i].Values = append([]string(nil), out[i].Values...)
	}
	return out
}

func encodeArchiveChunk(chunkID int, batch []packedSampleForArchive, opts BuildOptions) (ChunkIndex, []SampleIndex, []byte, error) {
	codec, err := newOptimizedArchiveCodec(batch)
	if err != nil {
		return ChunkIndex{}, nil, nil, err
	}
	uncompressed, sampleIndexes, err := codec.encodeChunk(chunkID, batch)
	if err != nil {
		return ChunkIndex{}, nil, nil, err
	}
	compressed, err := xzCompress(uncompressed, opts)
	if err != nil {
		return ChunkIndex{}, nil, nil, err
	}
	chunk := ChunkIndex{
		ID:               chunkID,
		CompressedSize:   int64(len(compressed)),
		UncompressedSize: int64(len(uncompressed)),
		TopKeys:          append([]string(nil), codec.TopKeys...),
		ValueSchemas:     append([]SchemaIndexEntry(nil), codec.ValueSchemas...),
		FeatureSchemas:   append([]SchemaIndexEntry(nil), codec.FeatureSchemas...),
		FeatureFields:    append([]string(nil), codec.FeatureFields...),
		FieldCodecs:      append([]FieldCodec(nil), codec.FieldCodecs...),
	}
	return chunk, sampleIndexes, compressed, nil
}

func makeArchiveChunks(packed []packedSampleForArchive, chunkSize int, opts BuildOptions) ([]ChunkIndex, []SampleIndex, [][]byte, error) {
	var chunks []ChunkIndex
	var samples []SampleIndex
	var payloads [][]byte
	var relativeOffset int64
	for chunkID, start := 0, 0; start < len(packed); chunkID, start = chunkID+1, start+chunkSize {
		end := start + chunkSize
		if end > len(packed) {
			end = len(packed)
		}
		chunk, sampleIndexes, compressed, err := encodeArchiveChunk(chunkID, packed[start:end], opts)
		if err != nil {
			return nil, nil, nil, err
		}
		for i := start; i < end; i++ {
			packed[i].reduced = nil
			packed[i].reducedRoot = nil
		}
		chunk.Offset = relativeOffset
		chunks = append(chunks, chunk)
		relativeOffset += int64(len(compressed))
		samples = append(samples, sampleIndexes...)
		payloads = append(payloads, compressed)
	}
	return chunks, samples, payloads, nil
}

type packedSampleForArchive struct {
	index       SampleIndex
	reduced     []byte
	reducedRoot map[string]any
}

func readChunk(ctx context.Context, file archiveRangeReader, chunkStart int64, chunk ChunkIndex, index ArchiveIndex, wanted []string) (map[string][]byte, error) {
	compressed := make([]byte, chunk.CompressedSize)
	if err := readFullAt(ctx, file, compressed, chunkStart+chunk.Offset); err != nil {
		return nil, err
	}
	uncompressed, err := xzDecompress(compressed)
	if err != nil {
		return nil, err
	}
	if int64(len(uncompressed)) != chunk.UncompressedSize {
		return nil, fmt.Errorf("chunk %d uncompressed size mismatch", chunk.ID)
	}
	if index.PayloadFormat == optimizedPayloadFormat {
		codec, err := optimizedCodecFromChunk(chunk)
		if err != nil {
			return nil, err
		}
		return codec.decodeChunk(uncompressed, wanted)
	}
	if index.PayloadFormat != "" {
		return nil, fmt.Errorf("unsupported bakpack payload format %q", index.PayloadFormat)
	}
	return decodeLegacyChunk(chunk.ID, uncompressed)
}

func decodeLegacyChunk(chunkID int, uncompressed []byte) (map[string][]byte, error) {
	reader := bytes.NewReader(uncompressed)
	count, err := readUvarint(reader)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for i := uint64(0); i < count; i++ {
		sample, err := readString(reader)
		if err != nil {
			return nil, err
		}
		data, err := readBytes(reader)
		if err != nil {
			return nil, err
		}
		out[sample] = data
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("chunk %d has trailing bytes", chunkID)
	}
	return out, nil
}

type archiveRangeReader interface {
	ReadAt(context.Context, []byte, int64) (int, error)
	Close() error
}

type localRangeReader struct {
	file *os.File
}

func (r localRangeReader) ReadAt(ctx context.Context, data []byte, offset int64) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	return r.file.ReadAt(data, offset)
}

func (r localRangeReader) Close() error {
	return r.file.Close()
}

type httpRangeReader struct {
	client *http.Client
	url    string
}

func (r httpRangeReader) ReadAt(ctx context.Context, data []byte, offset int64) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	end := offset + int64(len(data)) - 1
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, fmt.Errorf("%s did not honor Range request %q: HTTP %d", r.url, req.Header.Get("Range"), resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	n := copy(data, body)
	if n != len(data) {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}

func (r httpRangeReader) Close() error {
	return nil
}

func openArchive(ctx context.Context, path string, opts OpenArchiveOptions) (archiveRangeReader, ArchiveIndex, int64, error) {
	file, err := openArchiveRangeReader(path, opts)
	if err != nil {
		return nil, ArchiveIndex{}, 0, err
	}
	header := make([]byte, len(ArchiveMagic)+8)
	if err := readFullAt(ctx, file, header, 0); err != nil {
		file.Close()
		return nil, ArchiveIndex{}, 0, err
	}
	if string(header[:len(ArchiveMagic)]) != ArchiveMagic {
		file.Close()
		return nil, ArchiveIndex{}, 0, fmt.Errorf("not a bakpack archive")
	}
	indexLen := binary.LittleEndian.Uint64(header[len(ArchiveMagic):])
	if indexLen > uint64(int(^uint(0)>>1)) {
		file.Close()
		return nil, ArchiveIndex{}, 0, fmt.Errorf("archive index is too large")
	}
	indexBytes := make([]byte, indexLen)
	if err := readFullAt(ctx, file, indexBytes, int64(len(header))); err != nil {
		file.Close()
		return nil, ArchiveIndex{}, 0, err
	}
	if isXZ(indexBytes) {
		decompressed, err := xzDecompress(indexBytes)
		if err != nil {
			file.Close()
			return nil, ArchiveIndex{}, 0, err
		}
		indexBytes = decompressed
	}
	var index ArchiveIndex
	if err := json.Unmarshal(indexBytes, &index); err != nil {
		file.Close()
		return nil, ArchiveIndex{}, 0, err
	}
	if index.Format != "bakpack" || index.Version != ArchiveVersion {
		file.Close()
		return nil, ArchiveIndex{}, 0, fmt.Errorf("unsupported bakpack archive version")
	}
	return file, index, int64(len(ArchiveMagic)) + 8 + int64(indexLen), nil
}

func openArchiveRangeReader(path string, opts OpenArchiveOptions) (archiveRangeReader, error) {
	if isHTTPURL(path) {
		client := opts.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}
		return httpRangeReader{client: client, url: path}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return localRangeReader{file: file}, nil
}

func readFullAt(ctx context.Context, reader archiveRangeReader, data []byte, offset int64) error {
	n, err := reader.ReadAt(ctx, data, offset)
	if err != nil && err != io.EOF {
		return err
	}
	if n != len(data) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func isHTTPURL(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	return parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func verifyReduced(entry SampleIndex, reducedJSON []byte) error {
	canonical, err := JSONBytesCanonicalSHA256(reducedJSON)
	if err != nil {
		return err
	}
	if canonical != entry.ReducedJSONCanonicalSHA256 {
		return fmt.Errorf("sample %s reduced canonical SHA-256 mismatch", entry.SampleID)
	}
	return nil
}

func buildOrder(ctx context.Context, opts BuildOptions, annotations map[string]FileRecord) ([]string, error) {
	if len(opts.Order) > 0 {
		return validateOrder(opts.Order, annotations)
	}
	genomeOrder, err := opts.Genomes.Order(ctx)
	if err != nil {
		return nil, err
	}
	order := make([]string, 0, len(annotations))
	for _, sample := range genomeOrder {
		if _, ok := annotations[sample]; ok {
			order = append(order, sample)
		}
	}
	if len(order) != len(annotations) {
		return nil, fmt.Errorf("genome order did not include all annotation samples")
	}
	return order, nil
}

func buildOrderFromSources(ctx context.Context, opts BuildOptions) ([]string, error) {
	annotationOrder, err := opts.Annotations.Order(ctx)
	if err != nil {
		return nil, err
	}
	annotations := make(map[string]FileRecord, len(annotationOrder))
	for _, sample := range annotationOrder {
		if sample == "" {
			return nil, fmt.Errorf("annotation source contains an empty sample ID")
		}
		if _, exists := annotations[sample]; exists {
			return nil, fmt.Errorf("duplicate annotation sample %q", sample)
		}
		annotations[sample] = FileRecord{SampleID: sample}
	}
	if len(annotations) == 0 {
		return nil, fmt.Errorf("no annotation JSON files found")
	}
	return buildOrder(ctx, opts, annotations)
}

func validateOrder(order []string, annotations map[string]FileRecord) ([]string, error) {
	seen := map[string]bool{}
	for _, sample := range order {
		if _, ok := annotations[sample]; !ok {
			return nil, fmt.Errorf("order contains unknown sample %q", sample)
		}
		if seen[sample] {
			return nil, fmt.Errorf("order contains duplicate sample %q", sample)
		}
		seen[sample] = true
	}
	if len(seen) != len(annotations) {
		missing := make([]string, 0, len(annotations)-len(seen))
		for sample := range annotations {
			if !seen[sample] {
				missing = append(missing, sample)
			}
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("order missing annotation sample %q", missing[0])
	}
	return append([]string(nil), order...), nil
}

func packSampleFromSources(ctx context.Context, opts BuildOptions, sample string) (packedSampleForArchive, error) {
	annotation, err := opts.Annotations.Get(ctx, sample)
	if err != nil {
		return packedSampleForArchive{}, err
	}
	genomeRecord, err := opts.Genomes.Get(ctx, sample)
	if err != nil {
		return packedSampleForArchive{}, err
	}
	return packReducedSample(sample, annotation, genomeRecord)
}

func recordsBySample(records []FileRecord) (map[string]FileRecord, error) {
	out := map[string]FileRecord{}
	for _, record := range records {
		if record.SampleID == "" {
			return nil, fmt.Errorf("record %q has no sample ID", record.Name)
		}
		if _, exists := out[record.SampleID]; exists {
			return nil, fmt.Errorf("duplicate sample %q", record.SampleID)
		}
		out[record.SampleID] = record
	}
	return out, nil
}

func xzCompress(data []byte, opts BuildOptions) ([]byte, error) {
	threads := opts.XZThreads
	if threads <= 0 {
		threads = 1
	}
	cmd := exec.Command("xz", "-9e", fmt.Sprintf("-T%d", threads), "-c")
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("xz compression failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func xzDecompress(data []byte) ([]byte, error) {
	reader, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func isXZ(data []byte) bool {
	return len(data) >= 6 && bytes.Equal(data[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00})
}

func writeUvarint(w io.Writer, value uint64) {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	_, _ = w.Write(buf[:n])
}

func readUvarint(r io.ByteReader) (uint64, error) {
	return binary.ReadUvarint(r)
}

func writeString(w io.Writer, value string) {
	writeBytes(w, []byte(value))
}

func readString(r *bytes.Reader) (string, error) {
	data, err := readBytes(r)
	return string(data), err
}

func writeBytes(w io.Writer, data []byte) {
	writeUvarint(w, uint64(len(data)))
	_, _ = w.Write(data)
}

func readBytes(r *bytes.Reader) ([]byte, error) {
	length, err := readUvarint(r)
	if err != nil {
		return nil, err
	}
	if length > uint64(r.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, length)
	_, err = io.ReadFull(r, out)
	return out, err
}
