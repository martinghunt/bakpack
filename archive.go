package bakpack

import (
	"bytes"
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
	Format         string             `json:"format"`
	Version        int                `json:"version"`
	PayloadFormat  string             `json:"payload_format"`
	ChunkSize      int                `json:"chunk_size"`
	TopKeys        []string           `json:"top_keys,omitempty"`
	ValueSchemas   []SchemaIndexEntry `json:"value_schemas,omitempty"`
	FeatureSchemas []SchemaIndexEntry `json:"feature_schemas,omitempty"`
	FeatureFields  []string           `json:"feature_fields,omitempty"`
	FieldCodecs    []FieldCodec       `json:"field_codecs,omitempty"`
	Chunks         []ChunkIndex       `json:"chunks"`
	Samples        []SampleIndex      `json:"samples"`
}

type ChunkIndex struct {
	ID               int   `json:"id"`
	Offset           int64 `json:"offset"`
	CompressedSize   int64 `json:"compressed_size"`
	UncompressedSize int64 `json:"uncompressed_size"`
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
}

type ExtractOptions struct {
	ArchivePath string
	Genomes     FileSource
	Samples     []string
	OutputDir   string
	Reduced     bool
	Original    bool
	Genome      bool
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
		genome, err := ReadGenome(sample, genomeRecord.Name, genomeRecord.Bytes)
		if err != nil {
			return fmt.Errorf("%s: %w", sample, err)
		}
		reduced, err := ReduceBaktaJSON(annotation.Bytes, genome)
		if err != nil {
			return fmt.Errorf("%s: reduce Bakta JSON: %w", sample, err)
		}
		packed = append(packed, packedSampleForArchive{
			index: SampleIndex{
				SampleID:                    sample,
				AnnotationName:              annotation.Name,
				GenomeName:                  genomeRecord.Name,
				OriginalJSONCanonicalSHA256: reduced.Original.CanonicalSHA256,
				ReducedJSONCanonicalSHA256:  reduced.Reduced.CanonicalSHA256,
			},
			reduced: reduced.ReducedJSON,
		})
	}

	chunks, samples, chunkPayloads, codec, err := makeArchiveChunks(packed, chunkSize, opts)
	if err != nil {
		return err
	}
	index := ArchiveIndex{
		Format:         "bakpack",
		Version:        ArchiveVersion,
		PayloadFormat:  optimizedPayloadFormat,
		ChunkSize:      chunkSize,
		TopKeys:        codec.TopKeys,
		ValueSchemas:   codec.ValueSchemas,
		FeatureSchemas: codec.FeatureSchemas,
		FeatureFields:  codec.FeatureFields,
		FieldCodecs:    codec.FieldCodecs,
		Chunks:         chunks,
		Samples:        samples,
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

func ExtractArchive(ctx context.Context, opts ExtractOptions) error {
	if len(opts.Samples) == 0 {
		return fmt.Errorf("at least one sample is required")
	}
	if !opts.Reduced && !opts.Original && !opts.Genome {
		opts.Reduced = true
	}
	if (opts.Original || opts.Genome) && opts.Genomes == nil {
		return fmt.Errorf("genome source is required for original JSON or FASTA extraction")
	}
	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = "."
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}

	file, index, chunkStart, err := openArchive(ctx, opts.ArchivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	sampleIndex := map[string]SampleIndex{}
	for _, sample := range index.Samples {
		sampleIndex[sample.SampleID] = sample
	}
	chunkIndex := map[int]ChunkIndex{}
	for _, chunk := range index.Chunks {
		chunkIndex[chunk.ID] = chunk
	}

	samplesByChunk := map[int][]string{}
	for _, sample := range opts.Samples {
		entry, ok := sampleIndex[sample]
		if !ok {
			return fmt.Errorf("sample %q not found in archive", sample)
		}
		samplesByChunk[entry.ChunkID] = append(samplesByChunk[entry.ChunkID], sample)
	}

	for chunkID, samples := range samplesByChunk {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		chunk, ok := chunkIndex[chunkID]
		if !ok {
			return fmt.Errorf("chunk %d missing from index", chunkID)
		}
		reducedBySample, err := readChunk(file, chunkStart, chunk, index, samples)
		if err != nil {
			return err
		}
		for _, sample := range samples {
			entry := sampleIndex[sample]
			reducedJSON, ok := reducedBySample[sample]
			if !ok {
				return fmt.Errorf("sample %q missing from chunk %d", sample, chunkID)
			}
			if err := verifyReduced(entry, reducedJSON); err != nil {
				return err
			}
			var genome Genome
			if opts.Original || opts.Genome {
				record, err := opts.Genomes.Get(ctx, sample)
				if err != nil {
					return err
				}
				genome, err = ReadGenome(sample, record.Name, record.Bytes)
				if err != nil {
					return err
				}
				if opts.Genome {
					if err := os.WriteFile(filepath.Join(outputDir, sample+".fa"), genome.FASTABytes(80), 0o644); err != nil {
						return err
					}
				}
			}
			if opts.Reduced {
				if err := os.WriteFile(filepath.Join(outputDir, sample+".reduced.bakta.json"), reducedJSON, 0o644); err != nil {
					return err
				}
			}
			if opts.Original {
				restored, err := RestoreBaktaJSON(reducedJSON, genome)
				if err != nil {
					return err
				}
				if restored.Original.CanonicalSHA256 != entry.OriginalJSONCanonicalSHA256 {
					return fmt.Errorf("sample %s original canonical SHA-256 mismatch", sample)
				}
				if err := os.WriteFile(filepath.Join(outputDir, sample+".bakta.json"), restored.OriginalJSON, 0o644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func ReadArchiveIndex(path string) (ArchiveIndex, error) {
	file, index, _, err := openArchive(context.Background(), path)
	if err != nil {
		return ArchiveIndex{}, err
	}
	file.Close()
	return index, nil
}

func makeArchiveChunks(packed []packedSampleForArchive, chunkSize int, opts BuildOptions) ([]ChunkIndex, []SampleIndex, [][]byte, *optimizedArchiveCodec, error) {
	codec, err := newOptimizedArchiveCodec(packed)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var chunks []ChunkIndex
	var samples []SampleIndex
	var payloads [][]byte
	var relativeOffset int64
	for chunkID, start := 0, 0; start < len(packed); chunkID, start = chunkID+1, start+chunkSize {
		end := start + chunkSize
		if end > len(packed) {
			end = len(packed)
		}
		uncompressed, sampleIndexes, err := codec.encodeChunk(chunkID, packed[start:end])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		compressed, err := xzCompress(uncompressed, opts)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		chunks = append(chunks, ChunkIndex{
			ID:               chunkID,
			Offset:           relativeOffset,
			CompressedSize:   int64(len(compressed)),
			UncompressedSize: int64(len(uncompressed)),
		})
		relativeOffset += int64(len(compressed))
		samples = append(samples, sampleIndexes...)
		payloads = append(payloads, compressed)
	}
	return chunks, samples, payloads, codec, nil
}

type packedSampleForArchive struct {
	index       SampleIndex
	reduced     []byte
	reducedRoot map[string]any
}

func readChunk(file archiveRangeReader, chunkStart int64, chunk ChunkIndex, index ArchiveIndex, wanted []string) (map[string][]byte, error) {
	compressed := make([]byte, chunk.CompressedSize)
	if err := readFullAt(file, compressed, chunkStart+chunk.Offset); err != nil {
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
		codec, err := optimizedCodecFromIndex(index)
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
	ReadAt([]byte, int64) (int, error)
	Close() error
}

type httpRangeReader struct {
	ctx    context.Context
	client *http.Client
	url    string
}

func (r httpRangeReader) ReadAt(data []byte, offset int64) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	end := offset + int64(len(data)) - 1
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
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

func openArchive(ctx context.Context, path string) (archiveRangeReader, ArchiveIndex, int64, error) {
	file, err := openArchiveRangeReader(ctx, path)
	if err != nil {
		return nil, ArchiveIndex{}, 0, err
	}
	header := make([]byte, len(ArchiveMagic)+8)
	if err := readFullAt(file, header, 0); err != nil {
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
	if err := readFullAt(file, indexBytes, int64(len(header))); err != nil {
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

func openArchiveRangeReader(ctx context.Context, path string) (archiveRangeReader, error) {
	if isHTTPURL(path) {
		if ctx == nil {
			ctx = context.Background()
		}
		return httpRangeReader{ctx: ctx, client: http.DefaultClient, url: path}, nil
	}
	return os.Open(path)
}

func readFullAt(reader archiveRangeReader, data []byte, offset int64) error {
	n, err := reader.ReadAt(data, offset)
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
