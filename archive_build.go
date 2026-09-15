package bakpack

import (
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

func buildArchiveFromChunks(opts BuildOptions, chunkSize int, buildChunks func(io.Writer) ([]ChunkIndex, []SampleIndex, error)) error {
	chunkFile, err := os.CreateTemp(filepath.Dir(opts.OutputPath), buildTempPattern(opts.OutputPath, "chunks"))
	if err != nil {
		return err
	}
	defer os.Remove(chunkFile.Name())
	defer chunkFile.Close()

	chunks, samples, err := buildChunks(chunkFile)
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

type archiveChunkBatcher struct {
	opts           BuildOptions
	chunkSize      int
	chunkWriter    io.Writer
	chunks         []ChunkIndex
	samples        []SampleIndex
	batch          []packedSampleForArchive
	relativeOffset int64
	chunkID        int
}

func newArchiveChunkBatcher(opts BuildOptions, chunkSize int, chunkWriter io.Writer) *archiveChunkBatcher {
	return &archiveChunkBatcher{
		opts:        opts,
		chunkSize:   chunkSize,
		chunkWriter: chunkWriter,
	}
}

func (b *archiveChunkBatcher) add(packed packedSampleForArchive) error {
	b.batch = append(b.batch, packed)
	if len(b.batch) == b.chunkSize {
		return b.flush()
	}
	return nil
}

func (b *archiveChunkBatcher) flush() error {
	if len(b.batch) == 0 {
		return nil
	}
	chunk, sampleIndexes, compressed, err := encodeArchiveChunk(b.chunkID, b.batch, b.opts)
	if err != nil {
		return err
	}
	written, err := b.chunkWriter.Write(compressed)
	if err != nil {
		return err
	}
	if written != len(compressed) {
		return io.ErrShortWrite
	}
	chunk.Offset = b.relativeOffset
	b.chunks = append(b.chunks, chunk)
	b.relativeOffset += int64(len(compressed))
	b.samples = append(b.samples, sampleIndexes...)
	for i := range b.batch {
		b.batch[i].reduced = nil
		b.batch[i].reducedRoot = nil
	}
	b.batch = b.batch[:0]
	b.chunkID++
	return nil
}

func (b *archiveChunkBatcher) indexes() ([]ChunkIndex, []SampleIndex) {
	return b.chunks, b.samples
}

func buildArchiveFromIndexedSources(ctx context.Context, opts BuildOptions, chunkSize int) error {
	order, err := buildOrderFromSources(ctx, opts)
	if err != nil {
		return err
	}
	return buildArchiveFromChunks(opts, chunkSize, func(chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
		return makeArchiveChunksFromIndexedSources(ctx, opts, order, chunkSize, chunkWriter)
	})
}

func makeArchiveChunksFromIndexedSources(ctx context.Context, opts BuildOptions, order []string, chunkSize int, chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
	batcher := newArchiveChunkBatcher(opts, chunkSize, chunkWriter)

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
		if err := batcher.add(packed); err != nil {
			return nil, nil, err
		}
	}
	if err := batcher.flush(); err != nil {
		return nil, nil, err
	}
	chunks, samples := batcher.indexes()
	return chunks, samples, nil
}

func buildArchiveFromPairedTarXZ(ctx context.Context, opts BuildOptions, annotationsTar, genomesTar TarXZSource, chunkSize int) error {
	return buildArchiveFromChunks(opts, chunkSize, func(chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
		return makeArchiveChunksFromPairedTarXZ(ctx, opts, annotationsTar, genomesTar, chunkSize, chunkWriter)
	})
}

func makeArchiveChunksFromPairedTarXZ(ctx context.Context, opts BuildOptions, annotationsTar, genomesTar TarXZSource, chunkSize int, chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
	batcher := newArchiveChunkBatcher(opts, chunkSize, chunkWriter)

	if err := streamPairedTarXZRecords(ctx, annotationsTar, genomesTar, func(annotation, genomeRecord FileRecord) error {
		packed, err := packReducedSample(annotation.SampleID, annotation, genomeRecord)
		if err != nil {
			return err
		}
		return batcher.add(packed)
	}); err != nil {
		return nil, nil, err
	}
	if err := batcher.flush(); err != nil {
		return nil, nil, err
	}
	chunks, samples := batcher.indexes()
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
	spoolDir, err := os.MkdirTemp(filepath.Dir(opts.OutputPath), buildTempPattern(opts.OutputPath, "annotations"))
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

	return buildArchiveFromChunks(opts, chunkSize, func(chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
		return makeArchiveChunksFromSpooledAnnotations(ctx, opts, annotations, order, chunkSize, chunkWriter)
	})
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
	batcher := newArchiveChunkBatcher(opts, chunkSize, chunkWriter)

	err := forEachSpooledAnnotationSample(ctx, opts, annotations, order, func(packed packedSampleForArchive) error {
		if annotation, ok := annotations[packed.index.SampleID]; ok {
			_ = os.Remove(annotation.Path)
		}
		return batcher.add(packed)
	})
	if err != nil {
		return nil, nil, err
	}
	if err := batcher.flush(); err != nil {
		return nil, nil, err
	}
	chunks, samples := batcher.indexes()
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

// writeArchiveFile writes the archive to a temporary file beside path and
// renames it into place only once everything has been written successfully,
// so a failure partway through (disk full, process killed) leaves any
// previously existing archive at path untouched instead of truncated.
func writeArchiveFile(path string, index ArchiveIndex, opts BuildOptions, chunks io.Reader) error {
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	indexBytes, err = xzCompress(indexBytes, opts)
	if err != nil {
		return err
	}

	out, err := os.CreateTemp(filepath.Dir(path), buildTempPattern(path, "archive"))
	if err != nil {
		return err
	}
	tempPath := out.Name()
	renamed := false
	defer func() {
		out.Close()
		if !renamed {
			os.Remove(tempPath)
		}
	}()

	if _, err := out.Write([]byte(ArchiveMagic)); err != nil {
		return err
	}
	if err := binary.Write(out, binary.LittleEndian, uint64(len(indexBytes))); err != nil {
		return err
	}
	if _, err := out.Write(indexBytes); err != nil {
		return err
	}
	if _, err := io.Copy(out, chunks); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// buildTempPattern returns a visible, output-specific temporary name pattern.
// Keeping interrupted-build artifacts beside and named after their target makes
// them straightforward to identify and remove without affecting other builds.
func buildTempPattern(outputPath, purpose string) string {
	return filepath.Base(outputPath) + ".tmp-" + purpose + "-*"
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

type packedSampleForArchive struct {
	index       SampleIndex
	reduced     []byte
	reducedRoot map[string]any
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
