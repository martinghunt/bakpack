package bakpack

import (
	"io"
	"net/http"
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

	// AnnotationSpoolCompression controls temporary files that hold tar
	// members until they can be paired: annotations from a tar whose order
	// differs from the genome order, and genomes from a tar when Order is set.
	// Supported values are "", "gzip", "none", and "raw"; "" defaults to gzip.
	AnnotationSpoolCompression string

	// Warnings, if set, receives one line per sample whose sequences could
	// not be matched to its genome FASTA. Writes may come from several
	// goroutines, so it must be safe for concurrent use (os.Stderr is).
	Warnings io.Writer
}

// ExtractOptions configures one-shot extraction from an archive path or
// HTTP(S) URL to files on disk.
type ExtractOptions struct {
	// ArchivePath is a local .bakpack path or HTTP(S) URL.
	ArchivePath string
	// OpenOptions configures archive reads, including custom HTTP clients for
	// HTTP(S) archives.
	OpenOptions OpenArchiveOptions
	// Genomes is required when Original, Genome, or GFF3 output is requested.
	Genomes FileSource
	// Samples are the sample IDs to extract.
	Samples []string
	// OutputDir receives extracted files. If empty, "." is used.
	OutputDir string
	// Reduced writes SAMPLE.reduced.bakta.json.
	Reduced bool
	// Original writes SAMPLE.bakta.json.
	Original bool
	// Genome writes SAMPLE.fa.
	Genome bool
	// Convert writes the annotation in each listed format (FormatGFF3,
	// FormatGenBank, FormatEMBL) as SAMPLE.gff3, SAMPLE.gbff or SAMPLE.embl.
	Convert []string
	// ConvertOptions configures Convert. Its Format and Warn are ignored.
	ConvertOptions ConvertOptions
	// Warnings, if set, receives compliant GFF3 warnings, one line each.
	Warnings io.Writer
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
	// Convert returns the annotation in each listed format (FormatGFF3,
	// FormatGenBank, FormatEMBL), in ExtractedSample.Converted.
	Convert []string
	// ConvertOptions configures Convert. Its Format and Warn are ignored;
	// warnings go to Warnings.
	ConvertOptions ConvertOptions
	// Warnings, if set, receives compliant GFF3 warnings, one line each.
	// Samples may be extracted concurrently, so it must be safe for
	// concurrent use (os.Stderr is).
	Warnings io.Writer
	// OnSample is called for each extracted sample. When nil, Extract returns
	// accumulated results in the same order as Samples.
	OnSample func(ExtractedSample) error
}

// ExtractedSample contains the bytes extracted or reconstructed for one sample.
type ExtractedSample struct {
	SampleID       string
	AnnotationName string
	GenomeName     string
	ReducedJSON    []byte
	OriginalJSON   []byte
	GenomeFASTA    []byte
	// Converted holds the annotation in each format requested with
	// ExtractRequest.Convert, keyed by format.
	Converted                   map[string][]byte
	OriginalJSONCanonicalSHA256 string
	ReducedJSONCanonicalSHA256  string
}
