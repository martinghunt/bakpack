package bakpack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Build routes pair each annotation with its genome and pass the packed
// samples to the chunk batcher in archive order: the --order list if given,
// otherwise genome source order. Tar sources can only be read front to back,
// and finding one member means decompressing everything before it, so the
// routes read each tar source once and never look up a tar member by name.

// buildArchiveFromSamples writes an archive from the samples a route passes
// to add.
func buildArchiveFromSamples(ctx context.Context, opts BuildOptions, chunkSize int, pair func(add func(packedSampleForArchive) error) error) error {
	return buildArchiveFromChunks(ctx, opts, chunkSize, func(chunkWriter io.Writer) ([]ChunkIndex, []SampleIndex, error) {
		batcher := newArchiveChunkBatcher(ctx, opts, chunkSize, chunkWriter)
		count := 0
		err := pair(func(packed packedSampleForArchive) error {
			count++
			return batcher.add(packed)
		})
		if err != nil {
			return nil, nil, err
		}
		if count == 0 {
			return nil, nil, fmt.Errorf("no annotation JSON files found")
		}
		if err := batcher.flush(); err != nil {
			return nil, nil, err
		}
		chunks, samples := batcher.indexes()
		return chunks, samples, nil
	})
}

// pairTarSamples walks an annotation tar and a genome tar together while
// their sample IDs agree. At the first disagreement it spools the rest of
// the annotation tar to disk and finishes by streaming the rest of the
// genome tar, so each tar is read once whether or not their orders match.
func pairTarSamples(ctx context.Context, opts BuildOptions, annotationsTar, genomesTar tarSource, add func(packedSampleForArchive) error) error {
	annotationStream, err := newTarRecordStream(annotationsTar)
	if err != nil {
		return err
	}
	defer annotationStream.Close()
	genomeStream, err := newTarRecordStream(genomesTar)
	if err != nil {
		return err
	}
	defer genomeStream.Close()

	done := map[string]bool{}
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
		if !annotationOK || !genomeOK || annotation.SampleID != genome.SampleID {
			return pairTarSamplesAfterMismatch(ctx, opts, annotationStream, genomeStream, annotation, annotationOK, genome, genomeOK, genomesTar.Path, done, add)
		}
		if done[annotation.SampleID] {
			return fmt.Errorf("duplicate annotation sample %q", annotation.SampleID)
		}
		done[annotation.SampleID] = true
		packed, err := packReducedSample(opts, annotation.SampleID, annotation, genome)
		if err != nil {
			return err
		}
		if err := add(packed); err != nil {
			return err
		}
	}
}

func pairTarSamplesAfterMismatch(ctx context.Context, opts BuildOptions, annotationStream, genomeStream *tarRecordStream, annotation FileRecord, annotationOK bool, genome FileRecord, genomeOK bool, genomesPath string, done map[string]bool, add func(packedSampleForArchive) error) error {
	spool, err := newRecordSpool(opts, "annotation")
	if err != nil {
		return err
	}
	defer spool.close()
	for annotationOK {
		if done[annotation.SampleID] {
			return fmt.Errorf("duplicate annotation sample %q", annotation.SampleID)
		}
		if err := spool.add(annotation); err != nil {
			return err
		}
		annotation, annotationOK, err = annotationStream.Next(ctx)
		if err != nil {
			return err
		}
	}

	pending := genomeOK
	next := func() (FileRecord, bool, error) {
		if pending {
			pending = false
			return genome, true, nil
		}
		return genomeStream.Next(ctx)
	}
	return pairGenomeStream(opts, genomesPath, next, spool, done, add)
}

// pairSpooledAnnotationTarSamples spools an annotation tar to disk so its
// samples can be paired in genome or --order order.
func pairSpooledAnnotationTarSamples(ctx context.Context, opts BuildOptions, annotationsTar tarSource, add func(packedSampleForArchive) error) error {
	spool, err := newRecordSpool(opts, "annotation")
	if err != nil {
		return err
	}
	defer spool.close()
	if err := streamTarRecords(ctx, annotationsTar, spool.add); err != nil {
		return err
	}
	if spool.remaining() == 0 {
		return fmt.Errorf("no annotation JSON files found in %s", annotationsTar.Path)
	}
	return pairAnnotations(ctx, opts, spool, add)
}

// pairSourceAnnotationSamples pairs annotations from a source that can fetch
// samples by name, such as a directory, file list or manifest.
func pairSourceAnnotationSamples(ctx context.Context, opts BuildOptions, add func(packedSampleForArchive) error) error {
	order, err := opts.Annotations.Order(ctx)
	if err != nil {
		return err
	}
	annotations := &sourceAnnotations{ctx: ctx, source: opts.Annotations, ids: map[string]bool{}}
	for _, sample := range order {
		if sample == "" {
			return fmt.Errorf("annotation source contains an empty sample ID")
		}
		if annotations.ids[sample] {
			return fmt.Errorf("duplicate annotation sample %q", sample)
		}
		annotations.ids[sample] = true
	}
	if len(annotations.ids) == 0 {
		return fmt.Errorf("no annotation JSON files found")
	}
	return pairAnnotations(ctx, opts, annotations, add)
}

// pairAnnotations pairs every annotation with its genome in archive order.
func pairAnnotations(ctx context.Context, opts BuildOptions, annotations annotationSet, add func(packedSampleForArchive) error) error {
	genomesTar, genomesIsTar := asTarSource(opts.Genomes)
	if genomesIsTar && len(opts.Order) == 0 {
		stream, err := newTarRecordStream(genomesTar)
		if err != nil {
			return err
		}
		defer stream.Close()
		next := func() (FileRecord, bool, error) { return stream.Next(ctx) }
		return pairGenomeStream(opts, genomesTar.Path, next, annotations, map[string]bool{}, add)
	}

	order, err := buildOrder(ctx, opts, annotations.sampleIDs())
	if err != nil {
		return err
	}
	getGenome := opts.Genomes.Get
	if genomesIsTar {
		// --order differs from tar order, so spool the wanted genomes in one
		// pass rather than rescanning the tar for each sample.
		spool, err := newRecordSpool(opts, "genome")
		if err != nil {
			return err
		}
		defer spool.close()
		wanted := annotations.sampleIDs()
		err = streamTarRecords(ctx, genomesTar, func(genome FileRecord) error {
			if !wanted[genome.SampleID] {
				return nil
			}
			return spool.add(genome)
		})
		if err != nil {
			return err
		}
		getGenome = func(_ context.Context, sample string) (FileRecord, error) {
			return spool.take(sample)
		}
	}
	for _, sample := range order {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		annotation, err := annotations.take(sample)
		if err != nil {
			return err
		}
		genome, err := getGenome(ctx, sample)
		if err != nil {
			return err
		}
		packed, err := packReducedSample(opts, sample, annotation, genome)
		if err != nil {
			return err
		}
		if err := add(packed); err != nil {
			return err
		}
	}
	return nil
}

// pairGenomeStream reads genomes from next until it is exhausted, pairing
// each with its annotation. Genomes without an annotation are skipped. done
// holds samples already paired and gains each sample paired here.
func pairGenomeStream(opts BuildOptions, genomesPath string, next func() (FileRecord, bool, error), annotations annotationSet, done map[string]bool, add func(packedSampleForArchive) error) error {
	for {
		genome, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if done[genome.SampleID] {
			return fmt.Errorf("duplicate genome sample %q in %s", genome.SampleID, genomesPath)
		}
		if !annotations.has(genome.SampleID) {
			continue
		}
		annotation, err := annotations.take(genome.SampleID)
		if err != nil {
			return err
		}
		done[genome.SampleID] = true
		packed, err := packReducedSample(opts, genome.SampleID, annotation, genome)
		if err != nil {
			return err
		}
		if err := add(packed); err != nil {
			return err
		}
	}
	if missing := annotations.sampleIDs(); len(missing) != 0 {
		return fmt.Errorf("genome source did not include annotation sample %q", firstSorted(missing))
	}
	return nil
}

// annotationSet holds the annotations still waiting for their genome.
type annotationSet interface {
	has(sample string) bool
	// take returns a sample's annotation and removes it from the set.
	take(sample string) (FileRecord, error)
	sampleIDs() map[string]bool
}

type sourceAnnotations struct {
	ctx    context.Context
	source FileSource
	ids    map[string]bool
}

func (a *sourceAnnotations) has(sample string) bool {
	return a.ids[sample]
}

func (a *sourceAnnotations) take(sample string) (FileRecord, error) {
	if !a.ids[sample] {
		return FileRecord{}, fmt.Errorf("annotation for sample %q not found", sample)
	}
	record, err := a.source.Get(a.ctx, sample)
	if err != nil {
		return FileRecord{}, err
	}
	delete(a.ids, sample)
	return record, nil
}

func (a *sourceAnnotations) sampleIDs() map[string]bool {
	ids := make(map[string]bool, len(a.ids))
	for sample := range a.ids {
		ids[sample] = true
	}
	return ids
}

// recordSpool keeps tar members on disk until they are paired, one file per
// sample, deleting each file when it is taken.
type recordSpool struct {
	kind        string
	dir         string
	compression string
	count       int
	records     map[string]spooledRecord
}

type spooledRecord struct {
	name string
	path string
}

func newRecordSpool(opts BuildOptions, kind string) (*recordSpool, error) {
	compression, err := normalizeSpoolCompression(opts.AnnotationSpoolCompression)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(opts.OutputPath), buildTempPattern(opts.OutputPath, kind+"s"))
	if err != nil {
		return nil, err
	}
	return &recordSpool{kind: kind, dir: dir, compression: compression, records: map[string]spooledRecord{}}, nil
}

func (s *recordSpool) close() {
	os.RemoveAll(s.dir)
}

func (s *recordSpool) add(record FileRecord) error {
	if _, exists := s.records[record.SampleID]; exists {
		return fmt.Errorf("duplicate %s sample %q", s.kind, record.SampleID)
	}
	path := filepath.Join(s.dir, fmt.Sprintf("%06d%s", s.count, spoolFileSuffix(s.compression)))
	s.count++
	if err := writeSpoolFile(path, record.Bytes, s.compression); err != nil {
		return err
	}
	s.records[record.SampleID] = spooledRecord{name: record.Name, path: path}
	return nil
}

func (s *recordSpool) has(sample string) bool {
	_, ok := s.records[sample]
	return ok
}

func (s *recordSpool) take(sample string) (FileRecord, error) {
	record, ok := s.records[sample]
	if !ok {
		return FileRecord{}, fmt.Errorf("%s for sample %q not found", s.kind, sample)
	}
	data, err := readSpoolFile(record.path)
	if err != nil {
		return FileRecord{}, err
	}
	delete(s.records, sample)
	_ = os.Remove(record.path)
	return FileRecord{SampleID: sample, Name: record.name, Bytes: data}, nil
}

func (s *recordSpool) remaining() int {
	return len(s.records)
}

func (s *recordSpool) sampleIDs() map[string]bool {
	ids := make(map[string]bool, len(s.records))
	for sample := range s.records {
		ids[sample] = true
	}
	return ids
}

func firstSorted(ids map[string]bool) string {
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	return sorted[0]
}
