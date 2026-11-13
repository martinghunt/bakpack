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
	return buildArchiveFromSamples(ctx, opts, chunkSize, func(add func(packedSampleForArchive) error) error {
		annotationsTar, annotationsIsTar := asTarSource(opts.Annotations)
		genomesTar, genomesIsTar := asTarSource(opts.Genomes)
		switch {
		case annotationsIsTar && genomesIsTar && len(opts.Order) == 0:
			return pairTarSamples(ctx, opts, annotationsTar, genomesTar, add)
		case annotationsIsTar:
			return pairSpooledAnnotationTarSamples(ctx, opts, annotationsTar, add)
		default:
			return pairSourceAnnotationSamples(ctx, opts, add)
		}
	})
}

func buildArchiveFromChunks(ctx context.Context, opts BuildOptions, chunkSize int, buildChunks func(io.Writer) ([]ChunkIndex, []SampleIndex, error)) error {
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
	// Force any OS-buffered writes to storage now, so a delayed write
	// failure (e.g. on NFS, or a local filesystem near ENOSPC) surfaces here
	// instead of silently going unnoticed.
	if err := chunkFile.Sync(); err != nil {
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
	return writeArchiveFile(ctx, opts.OutputPath, index, opts, chunkFile)
}

type archiveChunkBatcher struct {
	ctx            context.Context
	opts           BuildOptions
	chunkSize      int
	chunkWriter    io.Writer
	chunks         []ChunkIndex
	samples        []SampleIndex
	batch          []packedSampleForArchive
	relativeOffset int64
	chunkID        int
}

func newArchiveChunkBatcher(ctx context.Context, opts BuildOptions, chunkSize int, chunkWriter io.Writer) *archiveChunkBatcher {
	return &archiveChunkBatcher{
		ctx:         ctx,
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
	chunk, sampleIndexes, compressed, err := encodeArchiveChunk(b.ctx, b.chunkID, b.batch, b.opts)
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
		return ".gz"
	}
	return ""
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

func packReducedSample(opts BuildOptions, sample string, annotation, genomeRecord FileRecord) (packedSampleForArchive, error) {
	genome, err := ReadGenome(sample, genomeRecord.Name, genomeRecord.Bytes)
	if err != nil {
		return packedSampleForArchive{}, fmt.Errorf("%s: %w", sample, err)
	}
	reduced, err := ReduceBaktaJSON(annotation.Bytes, genome)
	if err != nil {
		return packedSampleForArchive{}, fmt.Errorf("%s: reduce Bakta JSON: %w", sample, err)
	}
	if opts.Warnings != nil && len(reduced.UnmatchedSequences) != 0 {
		fmt.Fprintln(opts.Warnings, UnmatchedSequencesWarning(sample, reduced.UnmatchedSequences))
	}
	return packedSampleForArchive{
		index: SampleIndex{
			SampleID:                    sample,
			AnnotationName:              annotation.Name,
			GenomeName:                  genomeRecord.Name,
			OriginalJSONCanonicalSHA256: reduced.Original.CanonicalSHA256,
			ReducedJSONCanonicalSHA256:  reduced.Reduced.CanonicalSHA256,
		},
		reduced:     reduced.ReducedJSON,
		reducedRoot: reduced.reducedRoot,
	}, nil
}

func asTarSource(source FileSource) (tarSource, bool) {
	switch source := source.(type) {
	case TarXZSource:
		return source.tar(), true
	case *TarXZSource:
		return source.tar(), true
	case TarSource:
		return source.tar(), true
	case *TarSource:
		return source.tar(), true
	default:
		return tarSource{}, false
	}
}

// writeArchiveFile writes the archive to a temporary file beside path and
// renames it into place only once everything has been written successfully,
// so a failure partway through (disk full, process killed) leaves any
// previously existing archive at path untouched instead of truncated.
func writeArchiveFile(ctx context.Context, path string, index ArchiveIndex, opts BuildOptions, chunks io.Reader) error {
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	indexBytes, err = xzCompress(ctx, indexBytes, opts)
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

func encodeArchiveChunk(ctx context.Context, chunkID int, batch []packedSampleForArchive, opts BuildOptions) (ChunkIndex, []SampleIndex, []byte, error) {
	codec, err := newOptimizedArchiveCodec(batch)
	if err != nil {
		return ChunkIndex{}, nil, nil, err
	}
	uncompressed, sampleIndexes, err := codec.encodeChunk(chunkID, batch)
	if err != nil {
		return ChunkIndex{}, nil, nil, err
	}
	compressed, err := xzCompress(ctx, uncompressed, opts)
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

func buildOrder(ctx context.Context, opts BuildOptions, annotations map[string]bool) ([]string, error) {
	if len(opts.Order) > 0 {
		return validateOrder(opts.Order, annotations)
	}
	genomeOrder, err := opts.Genomes.Order(ctx)
	if err != nil {
		return nil, err
	}
	order := make([]string, 0, len(annotations))
	seen := map[string]bool{}
	for _, sample := range genomeOrder {
		if !annotations[sample] {
			continue
		}
		if seen[sample] {
			return nil, fmt.Errorf("duplicate genome sample %q", sample)
		}
		seen[sample] = true
		order = append(order, sample)
	}
	if len(order) != len(annotations) {
		return nil, fmt.Errorf("genome order did not include all annotation samples")
	}
	return order, nil
}

func validateOrder(order []string, annotations map[string]bool) ([]string, error) {
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
