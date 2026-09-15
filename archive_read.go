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
)

// OpenArchive opens a local .bakpack path or HTTP(S) URL. HTTP(S) archives are
// read with byte-range requests.
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
		if _, exists := archive.sampleIndex[sample.SampleID]; exists {
			reader.Close()
			return nil, fmt.Errorf("archive index has duplicate sample %q", sample.SampleID)
		}
		archive.sampleIndex[sample.SampleID] = sample
	}
	for _, chunk := range index.Chunks {
		if _, exists := archive.chunkIndex[chunk.ID]; exists {
			reader.Close()
			return nil, fmt.Errorf("archive index has duplicate chunk id %d", chunk.ID)
		}
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

// ReadArchiveIndex reads the front index from a local or HTTP(S) archive using
// a background context.
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

// maxArchiveComponentSize bounds any single length-prefixed archive
// component (the index, or a chunk's compressed payload) that gets
// allocated in one shot from a size field in untrusted archive data, so a
// corrupted or malicious value can't force an unbounded allocation attempt
// before the corresponding read is even performed.
const maxArchiveComponentSize = 1 << 30 // 1 GiB

func readChunk(ctx context.Context, file archiveRangeReader, chunkStart int64, chunk ChunkIndex, index ArchiveIndex, wanted []string) (map[string][]byte, error) {
	if chunk.CompressedSize < 0 || chunk.CompressedSize > maxArchiveComponentSize {
		return nil, fmt.Errorf("chunk %d has an invalid compressed size %d", chunk.ID, chunk.CompressedSize)
	}
	compressed := make([]byte, chunk.CompressedSize)
	if err := readFullAt(ctx, file, compressed, chunkStart+chunk.Offset); err != nil {
		return nil, err
	}
	uncompressed, err := xzDecompress(compressed, maxDecompressedComponentSize)
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
	// Read directly into data through a limited reader so a misbehaving or
	// malicious server can't send more than the requested range and force an
	// unbounded read into memory.
	n, err := io.ReadFull(io.LimitReader(resp.Body, int64(len(data))), data)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
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
	if indexLen > maxArchiveComponentSize {
		file.Close()
		return nil, ArchiveIndex{}, 0, fmt.Errorf("archive index is too large")
	}
	indexBytes := make([]byte, indexLen)
	if err := readFullAt(ctx, file, indexBytes, int64(len(header))); err != nil {
		file.Close()
		return nil, ArchiveIndex{}, 0, err
	}
	if isXZ(indexBytes) {
		decompressed, err := xzDecompress(indexBytes, maxDecompressedComponentSize)
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
