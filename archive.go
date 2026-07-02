package bakpack

import "net/http"

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
