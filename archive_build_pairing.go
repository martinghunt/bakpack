package bakpack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// samplePairing pairs each annotation with its genome and passes the pairs
// on in output order: Order if given, otherwise genome source order. Tar
// sources can only be read front to back, and finding one member means
// decompressing everything before it, so pairing reads each tar source once
// and never looks up a tar member by name.
type samplePairing struct {
	annotations FileSource
	genomes     FileSource
	order       []string
	// outputPath names the temporary spool directories, which are made
	// beside it.
	outputPath       string
	spoolCompression string
}

// pairFunc receives one paired sample.
type pairFunc func(sample string, annotation, genome FileRecord) error

// run pairs every annotation with its genome. It fails if no annotations
// are found.
func (p samplePairing) run(ctx context.Context, fn pairFunc) error {
	count := 0
	counted := func(sample string, annotation, genome FileRecord) error {
		count++
		return fn(sample, annotation, genome)
	}
	annotationsTar, annotationsIsTar := asTarSource(p.annotations)
	genomesTar, genomesIsTar := asTarSource(p.genomes)
	var err error
	switch {
	case annotationsIsTar && genomesIsTar && len(p.order) == 0:
		err = p.pairTars(ctx, annotationsTar, genomesTar, counted)
	case annotationsIsTar:
		err = p.pairSpooledAnnotationTar(ctx, annotationsTar, counted)
	default:
		err = p.pairSourceAnnotations(ctx, counted)
	}
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("no annotation JSON files found")
	}
	return nil
}

// pairTarSamples walks an annotation tar and a genome tar together while
// their sample IDs agree. At the first disagreement it spools the rest of
// the annotation tar to disk and finishes by streaming the rest of the
// genome tar, so each tar is read once whether or not their orders match.
func (p samplePairing) pairTars(ctx context.Context, annotationsTar, genomesTar tarSource, fn pairFunc) error {
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
			return p.pairTarsAfterMismatch(ctx, annotationStream, genomeStream, annotation, annotationOK, genome, genomeOK, genomesTar.Path, done, fn)
		}
		if done[annotation.SampleID] {
			return fmt.Errorf("duplicate annotation sample %q", annotation.SampleID)
		}
		done[annotation.SampleID] = true
		if err := fn(annotation.SampleID, annotation, genome); err != nil {
			return err
		}
	}
}

func (p samplePairing) pairTarsAfterMismatch(ctx context.Context, annotationStream, genomeStream *tarRecordStream, annotation FileRecord, annotationOK bool, genome FileRecord, genomeOK bool, genomesPath string, done map[string]bool, fn pairFunc) error {
	spool, err := p.newRecordSpool("annotation")
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
	return pairGenomeStream(genomesPath, next, spool, done, fn)
}

// pairSpooledAnnotationTar spools an annotation tar to disk so its samples
// can be paired in genome or Order order.
func (p samplePairing) pairSpooledAnnotationTar(ctx context.Context, annotationsTar tarSource, fn pairFunc) error {
	spool, err := p.newRecordSpool("annotation")
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
	return p.pairAnnotations(ctx, spool, fn)
}

// pairSourceAnnotations pairs annotations from a source that can fetch
// samples by name, such as a directory, file list or manifest.
func (p samplePairing) pairSourceAnnotations(ctx context.Context, fn pairFunc) error {
	order, err := p.annotations.Order(ctx)
	if err != nil {
		return err
	}
	annotations := &sourceAnnotations{ctx: ctx, source: p.annotations, ids: map[string]bool{}}
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
	return p.pairAnnotations(ctx, annotations, fn)
}

// pairAnnotations pairs every annotation with its genome in archive order.
func (p samplePairing) pairAnnotations(ctx context.Context, annotations annotationSet, fn pairFunc) error {
	genomesTar, genomesIsTar := asTarSource(p.genomes)
	if genomesIsTar && len(p.order) == 0 {
		stream, err := newTarRecordStream(genomesTar)
		if err != nil {
			return err
		}
		defer stream.Close()
		next := func() (FileRecord, bool, error) { return stream.Next(ctx) }
		return pairGenomeStream(genomesTar.Path, next, annotations, map[string]bool{}, fn)
	}

	order, err := pairingOrder(ctx, p.order, p.genomes, annotations.sampleIDs())
	if err != nil {
		return err
	}
	getGenome := p.genomes.Get
	if genomesIsTar {
		// Order differs from tar order, so spool the wanted genomes in one
		// pass rather than rescanning the tar for each sample.
		spool, err := p.newRecordSpool("genome")
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
		if err := fn(sample, annotation, genome); err != nil {
			return err
		}
	}
	return nil
}

// pairGenomeStream reads genomes from next until it is exhausted, pairing
// each with its annotation. Genomes without an annotation are skipped. done
// holds samples already paired and gains each sample paired here.
func pairGenomeStream(genomesPath string, next func() (FileRecord, bool, error), annotations annotationSet, done map[string]bool, fn pairFunc) error {
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
		if err := fn(genome.SampleID, annotation, genome); err != nil {
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

func (p samplePairing) newRecordSpool(kind string) (*recordSpool, error) {
	compression, err := normalizeSpoolCompression(p.spoolCompression)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(p.outputPath), buildTempPattern(p.outputPath, kind+"s"))
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
