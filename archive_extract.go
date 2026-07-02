package bakpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

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

type archiveExtractPlan struct {
	chunkOrder     []int
	samplesByChunk map[int][]string
}

func normalizeExtractRequest(ctx context.Context, req ExtractRequest) (context.Context, ExtractRequest, error) {
	if req.GFF3AnnotationOnly {
		req.GFF3 = true
	}
	if !req.Reduced && !req.Original && !req.Genome && !req.GFF3 {
		req.Reduced = true
	}
	if len(req.Samples) > 0 && extractRequiresGenome(req) && req.Genomes == nil {
		return nil, ExtractRequest{}, fmt.Errorf("genome source is required for original JSON, FASTA, or GFF3 extraction")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx, req, nil
}

func extractRequiresGenome(req ExtractRequest) bool {
	return req.Original || req.Genome || req.GFF3
}

func (a *Archive) planExtractChunks(samples []string) (archiveExtractPlan, error) {
	plan := archiveExtractPlan{
		samplesByChunk: map[int][]string{},
	}
	chunkOrderSet := map[int]bool{}
	for _, sample := range samples {
		entry, ok := a.sampleIndex[sample]
		if !ok {
			return archiveExtractPlan{}, fmt.Errorf("sample %q not found in archive", sample)
		}
		plan.samplesByChunk[entry.ChunkID] = append(plan.samplesByChunk[entry.ChunkID], sample)
		if !chunkOrderSet[entry.ChunkID] {
			chunkOrderSet[entry.ChunkID] = true
			plan.chunkOrder = append(plan.chunkOrder, entry.ChunkID)
		}
	}
	sort.Ints(plan.chunkOrder)
	return plan, nil
}

func makeExtractGenomeProvider(ctx context.Context, req ExtractRequest) (genomeRecordProvider, error) {
	if !extractRequiresGenome(req) {
		return nil, nil
	}
	return newGenomeRecordProvider(ctx, req.Genomes, req.Samples)
}

func (a *Archive) extractChunkSamples(ctx context.Context, req ExtractRequest, plan archiveExtractPlan, chunkID int, genomes genomeRecordProvider, emit func(ExtractedSample) error) error {
	chunk, ok := a.chunkIndex[chunkID]
	if !ok {
		return fmt.Errorf("chunk %d missing from index", chunkID)
	}
	samples := plan.samplesByChunk[chunkID]
	reducedBySample, err := readChunk(ctx, a.reader, a.chunkStart, chunk, a.index, samples)
	if err != nil {
		return err
	}
	for _, sample := range samples {
		entry := a.sampleIndex[sample]
		reducedJSON, ok := reducedBySample[sample]
		if !ok {
			return fmt.Errorf("sample %q missing from chunk %d", sample, chunkID)
		}
		result, err := buildExtractedSample(ctx, req, genomes, entry, reducedJSON)
		if err != nil {
			return err
		}
		if err := emit(result); err != nil {
			return err
		}
	}
	return nil
}

func buildExtractedSample(ctx context.Context, req ExtractRequest, genomes genomeRecordProvider, entry SampleIndex, reducedJSON []byte) (ExtractedSample, error) {
	if err := verifyReduced(entry, reducedJSON); err != nil {
		return ExtractedSample{}, err
	}
	if err := validateBaktaJSONFeatureTypes(reducedJSON); err != nil {
		return ExtractedSample{}, fmt.Errorf("%s: %w", entry.SampleID, err)
	}
	result := ExtractedSample{
		SampleID:                    entry.SampleID,
		AnnotationName:              entry.AnnotationName,
		GenomeName:                  entry.GenomeName,
		OriginalJSONCanonicalSHA256: entry.OriginalJSONCanonicalSHA256,
		ReducedJSONCanonicalSHA256:  entry.ReducedJSONCanonicalSHA256,
	}
	if req.Reduced {
		result.ReducedJSON = append([]byte(nil), reducedJSON...)
	}
	var genome Genome
	if extractRequiresGenome(req) {
		record, err := genomes.Get(ctx, entry.SampleID)
		if err != nil {
			return ExtractedSample{}, err
		}
		genome, err = ReadGenome(entry.SampleID, record.Name, record.Bytes)
		if err != nil {
			return ExtractedSample{}, err
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
			return ExtractedSample{}, err
		}
		result.GFF3 = gff3
	}
	if req.Original {
		restored, err := RestoreBaktaJSON(reducedJSON, genome)
		if err != nil {
			return ExtractedSample{}, err
		}
		if restored.Original.CanonicalSHA256 != entry.OriginalJSONCanonicalSHA256 {
			return ExtractedSample{}, fmt.Errorf("sample %s original canonical SHA-256 mismatch", entry.SampleID)
		}
		result.OriginalJSON = restored.OriginalJSON
	}
	return result, nil
}

func collectExtractedSamples(samples []string, resultsBySample map[string]ExtractedSample) []ExtractedSample {
	results := make([]ExtractedSample, 0, len(samples))
	for _, sample := range samples {
		results = append(results, resultsBySample[sample])
	}
	return results
}

func extractRequestFromOptions(opts ExtractOptions) ExtractRequest {
	return ExtractRequest{
		Genomes:            opts.Genomes,
		Samples:            opts.Samples,
		Reduced:            opts.Reduced,
		Original:           opts.Original,
		Genome:             opts.Genome,
		GFF3:               opts.GFF3,
		GFF3AnnotationOnly: opts.GFF3AnnotationOnly,
	}
}

func writeExtractedSampleOutputs(outputDir string, req ExtractRequest, sample ExtractedSample) error {
	if req.Genome {
		if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".fa"), sample.GenomeFASTA, 0o644); err != nil {
			return err
		}
	}
	if req.Reduced {
		if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".reduced.bakta.json"), sample.ReducedJSON, 0o644); err != nil {
			return err
		}
	}
	if req.Original {
		if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".bakta.json"), sample.OriginalJSON, 0o644); err != nil {
			return err
		}
	}
	if req.GFF3 {
		if err := os.WriteFile(filepath.Join(outputDir, sample.SampleID+".gff3"), sample.GFF3, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Extract extracts one or more samples from the archive. Without OnSample,
// results are returned in the same order as req.Samples. With OnSample, results
// are delivered to the callback and the returned slice is nil.
func (a *Archive) Extract(ctx context.Context, req ExtractRequest) ([]ExtractedSample, error) {
	if a == nil || a.reader == nil {
		return nil, fmt.Errorf("archive is closed or nil")
	}
	var err error
	ctx, req, err = normalizeExtractRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(req.Samples) == 0 {
		return nil, nil
	}

	plan, err := a.planExtractChunks(req.Samples)
	if err != nil {
		return nil, err
	}
	genomes, err := makeExtractGenomeProvider(ctx, req)
	if err != nil {
		return nil, err
	}

	resultsBySample := map[string]ExtractedSample{}
	emit := func(result ExtractedSample) error {
		if req.OnSample != nil {
			return req.OnSample(result)
		}
		resultsBySample[result.SampleID] = result
		return nil
	}
	for _, chunkID := range plan.chunkOrder {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if err := a.extractChunkSamples(ctx, req, plan, chunkID, genomes, emit); err != nil {
			return nil, err
		}
	}
	if req.OnSample != nil {
		return nil, nil
	}
	return collectExtractedSamples(req.Samples, resultsBySample), nil
}

func ExtractArchive(ctx context.Context, opts ExtractOptions) error {
	req := extractRequestFromOptions(opts)
	var err error
	ctx, req, err = normalizeExtractRequest(ctx, req)
	if err != nil {
		return err
	}
	if len(req.Samples) == 0 {
		return nil
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

	req.OnSample = func(sample ExtractedSample) error {
		return writeExtractedSampleOutputs(outputDir, req, sample)
	}
	_, err = archive.Extract(ctx, req)
	return err
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
