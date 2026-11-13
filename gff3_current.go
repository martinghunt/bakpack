package bakpack

// This file writes GFF3 in the format Bakta 1.10 and later writes, in its
// default and --compliant modes, as described in docs/bakta-gff3-format.md.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

var (
	baktaVersionPattern       = regexp.MustCompile(`^v?(\d+)\.(\d+)`)
	uncharacterizedPattern    = regexp.MustCompile(`(?i)uncharacteri[sz]ed`)
	insdcNcRNAGenePattern     = regexp.MustCompile(`^[a-z]{3}[A-Z][0-9]?$`)
	insdcSequenceIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_.:*#-]{1,25}$`)
	insdcLocusTagPrefixRegexp = regexp.MustCompile(`^[A-Z][A-Z0-9]{2,11}$`)
)

// insdcDbxrefDatabases are the db_xrefs databases INSDC accepts as Dbxref in
// compliant mode; other db_xrefs are written as Note.
var insdcDbxrefDatabases = map[string]bool{
	"UniProtKB/TrEMBL": true,
	"GO":               true,
	"Pfam":             true,
	"RFAM":             true,
}

var ncRNAClassesBySOID = map[string]string{
	"SO:0000644": "antisense_RNA",
	"SO:0000374": "ribozyme",
	"SO:0000386": "RNase_P_RNA",
}

var regulatoryClassesBySOID = map[string]string{
	"SO:0000140": "attenuator",
	"SO:0000035": "riboswitch",
	"SO:0002168": "response_element",
	"SO:1001268": "recoding_stimulatory_region",
	"SO:0001427": "recoding_stimulatory_region",
	"SO:0000139": "ribosome_binding_site",
}

// minCompliantSequenceLength is the shortest sequence Bakta --compliant keeps.
const minCompliantSequenceLength = 200

// usesCurrentBaktaGFF3Style reports whether data is JSON from Bakta 1.10 or
// later, by version.bakta when it has a MAJOR.MINOR prefix and by the JSON
// layout otherwise.
func usesCurrentBaktaGFF3Style(data map[string]any) bool {
	version, _ := data["version"].(map[string]any)
	if baktaVersion, ok := version["bakta"].(string); ok {
		if match := baktaVersionPattern.FindStringSubmatch(baktaVersion); match != nil {
			major, majorErr := strconv.Atoi(match[1])
			minor, minorErr := strconv.Atoi(match[2])
			if majorErr == nil && minorErr == nil {
				return major > 1 || (major == 1 && minor >= 10)
			}
		}
	}
	return usesCurrentBaktaLayout(data)
}

// currentGFF3Writer holds the state for writing one GFF3 file.
type currentGFF3Writer struct {
	buf       bytes.Buffer
	compliant bool
	// lengths maps sequence IDs to their lengths, for features with edge.
	lengths map[string]int
}

// writeCurrentBaktaGFF3 renders data as GFF3 in the Bakta >= 1.10 format.
func writeCurrentBaktaGFF3(data map[string]any, opts BaktaGFF3Options) ([]byte, error) {
	w := &currentGFF3Writer{compliant: opts.Compliant, lengths: map[string]int{}}
	sequences := mapSlice(data["sequences"])
	featuresByContig, featureOrder := gffFeaturesByContig(data)

	// seen holds every sequence ID in sequences, including those compliant
	// mode leaves out, so that their features are not written afterwards.
	seen := map[string]bool{}
	for _, sequence := range sequences {
		seen[pyStr(sequence["id"])] = true
	}
	if opts.Compliant {
		sequences = dropShortSequences(sequences, featuresByContig, opts.Warn)
		warnNonCompliantIDs(sequences, seen, featuresByContig, featureOrder, opts.Warn)
	}

	w.writeHeader(data, opts.bakpackVersion())
	for _, sequence := range sequences {
		id := pyStr(sequence["id"])
		if length, ok := jsonInt(sequence["length"]); ok {
			w.lengths[id] = length
		}
	}
	for _, sequence := range sequences {
		id := pyStr(sequence["id"])
		w.writeRegion(sequence)
		for _, feature := range featuresByContig[id] {
			if err := w.writeFeature(feature); err != nil {
				return nil, err
			}
		}
	}
	for _, contig := range featureOrder {
		if seen[contig] {
			continue
		}
		for _, feature := range featuresByContig[contig] {
			if err := w.writeFeature(feature); err != nil {
				return nil, err
			}
		}
	}
	if !opts.Compliant && !opts.AnnotationOnly {
		w.writeFASTA(sequences, sequenceNTKey(usesCurrentBaktaLayout(data)))
	}
	return w.buf.Bytes(), nil
}

// dropShortSequences returns sequences without those shorter than Bakta
// --compliant keeps, and reports what was left out.
func dropShortSequences(sequences []map[string]any, featuresByContig map[string][]map[string]any, warn func(string)) []map[string]any {
	var kept []map[string]any
	var dropped []string
	droppedFeatures := 0
	for _, sequence := range sequences {
		length, ok := jsonInt(sequence["length"])
		if ok && length < minCompliantSequenceLength {
			id := pyStr(sequence["id"])
			dropped = append(dropped, id)
			droppedFeatures += len(featuresByContig[id])
			continue
		}
		kept = append(kept, sequence)
	}
	if len(dropped) != 0 && warn != nil {
		warn(fmt.Sprintf("left out %d contig(s) shorter than %d bp and their %d feature(s), as Bakta --compliant does: %s",
			len(dropped), minCompliantSequenceLength, droppedFeatures, warningList(dropped)))
	}
	return kept
}

// warnNonCompliantIDs reports kept sequence IDs and locus tag prefixes of
// written features that INSDC rejects, which compliant mode cannot fix. seen
// holds every sequence ID in the JSON's sequences.
func warnNonCompliantIDs(sequences []map[string]any, seen map[string]bool, featuresByContig map[string][]map[string]any, featureOrder []string, warn func(string)) {
	if warn == nil {
		return
	}
	var badIDs []string
	for _, sequence := range sequences {
		id := pyStr(sequence["id"])
		if !insdcSequenceIDPattern.MatchString(id) {
			badIDs = append(badIDs, id)
		}
	}
	if len(badIDs) != 0 {
		warn(fmt.Sprintf("%d contig ID(s) are not INSDC compliant (at most 25 characters from A-Z a-z 0-9 _ . : * # -): %s",
			len(badIDs), warningList(badIDs)))
	}

	// Features are checked in the order they are written: kept sequences
	// first, then sequence IDs not in sequences.
	contigs := make([]string, 0, len(sequences)+len(featureOrder))
	for _, sequence := range sequences {
		contigs = append(contigs, pyStr(sequence["id"]))
	}
	for _, contig := range featureOrder {
		if !seen[contig] {
			contigs = append(contigs, contig)
		}
	}
	seenPrefixes := map[string]bool{}
	var badPrefixes []string
	for _, contig := range contigs {
		for _, feature := range featuresByContig[contig] {
			locus, ok := feature["locus"].(string)
			if !ok {
				continue
			}
			prefix := locus
			if i := strings.LastIndex(locus, "_"); i >= 0 {
				prefix = locus[:i]
			}
			if seenPrefixes[prefix] {
				continue
			}
			seenPrefixes[prefix] = true
			if !insdcLocusTagPrefixRegexp.MatchString(prefix) {
				badPrefixes = append(badPrefixes, prefix)
			}
		}
	}
	if len(badPrefixes) != 0 {
		warn("locus tag prefix(es) are not INSDC compliant (3-12 upper case letters and digits, starting with a letter): " + warningList(badPrefixes))
	}
}

// warningList joins the first five items, adding ", ..." when there are more.
func warningList(items []string) string {
	if len(items) <= 5 {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:5], ", ") + ", ..."
}

func (w *currentGFF3Writer) writeHeader(data map[string]any, bakpackVersion string) {
	w.buf.WriteString("##gff-version 3\n")
	w.buf.WriteString("##feature-ontology https://github.com/The-Sequence-Ontology/SO-Ontologies/blob/v3.1/so.obo\n")
	fmt.Fprintf(&w.buf, "# Generated by bakpack %s\n", bakpackVersion)
	genome, _ := data["genome"].(map[string]any)
	if taxon := genome["taxon"]; pyTruthy(taxon) {
		fmt.Fprintf(&w.buf, "# organism %s\n", pyStr(taxon))
	}
	w.buf.WriteString("# Annotated with Bakta\n")
	version, _ := data["version"].(map[string]any)
	if baktaVersion, ok := version["bakta"]; ok {
		fmt.Fprintf(&w.buf, "# Software: v%s\n", pyStr(baktaVersion))
	}
	if db, ok := version["db"].(map[string]any); ok {
		parts := strings.Split(pyStr(db["version"]), ".")
		if len(parts) > 2 {
			parts = parts[:2]
		}
		fmt.Fprintf(&w.buf, "# Database: v%s, %s\n", strings.Join(parts, "."), pyStr(db["type"]))
	}
	w.buf.WriteString("# DOI: 10.1099/mgen.0.000685\n")
	w.buf.WriteString("# URL: github.com/oschwengers/bakta\n")
}

func (w *currentGFF3Writer) writeRegion(sequence map[string]any) {
	id := pyStr(sequence["id"])
	length := pyStr(sequence["length"])
	fmt.Fprintf(&w.buf, "##sequence-region %s 1 %s\n", id, length)
	attrs := &baktaAttrs{}
	attrs.set("ID", id)
	attrs.set("Name", id)
	if topology, _ := sequence["topology"].(string); topology == "circular" {
		attrs.set("Is_circular", "true")
	}
	w.writeLine(id, "Bakta", "region", "1", length, ".", "+", ".", attrs)
}

func (w *currentGFF3Writer) writeFASTA(sequences []map[string]any, ntKey string) {
	w.buf.WriteString("##FASTA\n")
	for _, sequence := range sequences {
		fmt.Fprintf(&w.buf, ">%s\n", pyStr(sequence["id"]))
		nt, _ := sequence[ntKey].(string)
		if nt == "" {
			w.buf.WriteByte('\n')
			continue
		}
		writeWrappedSequence(&w.buf, nt, 60)
	}
}

func (w *currentGFF3Writer) writeLine(seqID, source, featureType, start, stop, score, strand, phase string, attrs *baktaAttrs) {
	fmt.Fprintf(&w.buf, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		seqID, source, featureType, start, stop, score, strand, phase, attrs.String())
}

// featureLine holds the columns shared by a feature's line and, in compliant
// mode, its gene line.
type featureLine struct {
	seqID  string
	start  string
	stop   string
	strand string
}

func (w *currentGFF3Writer) featureLine(feature map[string]any) (featureLine, error) {
	seqID, _ := featureContig(feature)
	start, startOK := jsonInt(feature["start"])
	stop, stopOK := jsonInt(feature["stop"])
	if seqID == "" || !startOK || !stopOK {
		return featureLine{}, fmt.Errorf("feature %s is missing contig, start, or stop", gffFeatureID(feature))
	}
	if _, ok := feature["edge"]; ok {
		stop += w.lengths[seqID]
	}
	strand := "."
	if value, ok := feature["strand"]; ok {
		strand = pyStr(value)
	}
	return featureLine{seqID: seqID, start: strconv.Itoa(start), stop: strconv.Itoa(stop), strand: strand}, nil
}

func (w *currentGFF3Writer) write(line featureLine, source, featureType, score, phase string, attrs *baktaAttrs) {
	w.writeLine(line.seqID, source, featureType, line.start, line.stop, score, line.strand, phase, attrs)
}

// writeGene writes a compliant mode gene line for line.
func (w *currentGFF3Writer) writeGene(line featureLine, source string, attrs *baktaAttrs) {
	w.write(line, source, "gene", ".", ".", attrs)
}

func (w *currentGFF3Writer) writeFeature(feature map[string]any) error {
	featureType, _ := feature["type"].(string)
	if _, ok := baktaFeatureType(featureType); !ok {
		return unsupportedBaktaFeatureTypeError(featureType)
	}
	line, err := w.featureLine(feature)
	if err != nil {
		return err
	}
	switch featureType {
	case "tRNA":
		w.writeTRNA(feature, line)
	case "tmRNA":
		w.writeTmRNA(feature, line)
	case "rRNA", "ncRNA":
		w.writeInfernalRNA(feature, line, featureType)
	case "ncRNA-region":
		w.writeRegulatoryRegion(feature, line)
	case "crispr":
		return w.writeCRISPR(feature, line)
	case "cds":
		w.writeCDS(feature, line)
	case "sorf":
		w.writeSORF(feature, line)
	case "gap":
		w.writeGap(feature, line)
	case "oriC", "oriV", "oriT":
		w.writeOrigin(feature, line, featureType)
	default:
		return unsupportedBaktaFeatureTypeError(featureType)
	}
	return nil
}

func (w *currentGFF3Writer) writeTRNA(feature map[string]any, line featureLine) {
	locus := featureLocus(feature)
	attrs := locusAttrs(feature, locus, pyStr(feature["product"]))
	attrs.set("product", pyStr(feature["product"]))
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	setIfTruthy(attrs, feature, "gene", "gene")
	if _, ok := feature["pseudogene"]; ok {
		attrs.set("pseudogene", "unknown")
	} else if _, ok := feature["truncated"]; ok {
		attrs.set("pseudo", "True")
	}
	setIfTruthy(attrs, feature, "anti_codon", "anti_codon")
	setIfTruthy(attrs, feature, "amino_acid", "amino_acid")
	if w.compliant {
		attrs.set("Parent", locus+"_gene")
		attrs.set("inference", "profile:tRNAscan:2.0")
		setINSDCDbxrefs(attrs, feature)

		gene := geneAttrs(locus)
		setIfTruthy(gene, feature, "gene", "gene")
		if _, ok := feature["pseudogene"]; ok {
			gene.set("pseudogene", "unknown")
		}
		w.writeGene(line, "tRNAscan-SE", gene)
	}
	w.write(line, "tRNAscan-SE", "tRNA", ".", ".", attrs)
}

func (w *currentGFF3Writer) writeTmRNA(feature map[string]any, line featureLine) {
	locus := featureLocus(feature)
	attrs := locusAttrs(feature, locus, pyStr(feature["product"]))
	attrs.set("gene", pyStr(feature["gene"]))
	attrs.set("product", pyStr(feature["product"]))
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	tag, hasTag := feature["tag"].(map[string]any)
	if hasTag {
		attrs.set("tag_peptide", pyStr(tag["aa"]))
	}
	setTruncatedPseudo(attrs, feature)
	if w.compliant {
		attrs.set("Parent", locus+"_gene")
		attrs.set("inference", "profile:aragorn:1.2")
		setINSDCDbxrefs(attrs, feature)
		if hasTag {
			location := pyStr(tag["start"]) + ".." + pyStr(tag["stop"])
			if line.strand != "+" {
				location = "complement(" + location + ")"
			}
			attrs.set("tag_peptide", location)
		}
		w.writeGene(line, "Aragorn", rnaGeneAttrs(feature, locus))
	}
	w.write(line, "Aragorn", "tmRNA", ".", ".", attrs)
}

// writeInfernalRNA writes rRNA and ncRNA features, which share attributes.
func (w *currentGFF3Writer) writeInfernalRNA(feature map[string]any, line featureLine, featureType string) {
	locus := featureLocus(feature)
	attrs := locusAttrs(feature, locus, pyStr(feature["product"]))
	attrs.set("gene", pyStr(feature["gene"]))
	attrs.set("product", pyStr(feature["product"]))
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	setTruncatedPseudo(attrs, feature)
	if w.compliant {
		attrs.set("Parent", locus+"_gene")
		setINSDCDbxrefs(attrs, feature)
		if featureType == "ncRNA" {
			attrs.set("ncRNA_class", classBySOID(feature, ncRNAClassesBySOID))
		}
		setRfamInference(attrs)
		if featureType == "ncRNA" {
			if gene, ok := feature["gene"].(string); !ok || !insdcNcRNAGenePattern.MatchString(gene) {
				attrs.remove("gene")
			}
		}
		w.writeGene(line, "Infernal", rnaGeneAttrs(feature, locus))
	}
	w.write(line, "Infernal", featureType, pyStr(feature["evalue"]), ".", attrs)
}

func (w *currentGFF3Writer) writeRegulatoryRegion(feature map[string]any, line featureLine) {
	attrs := &baktaAttrs{}
	attrs.set("ID", featureIDValue(feature))
	attrs.set("Name", pyStr(feature["product"]))
	attrs.set("product", pyStr(feature["product"]))
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	setTruncatedPseudo(attrs, feature)
	if w.compliant {
		setRfamInference(attrs)
		setINSDCDbxrefs(attrs, feature)
		attrs.set("regulatory_class", classBySOID(feature, regulatoryClassesBySOID))
	}
	w.write(line, "Infernal", "regulatory_region", pyStr(feature["evalue"]), ".", attrs)
}

func (w *currentGFF3Writer) writeCRISPR(feature map[string]any, line featureLine) error {
	id := featureIDValue(feature)
	attrs := &baktaAttrs{}
	attrs.set("ID", id)
	attrs.set("Name", pyStr(feature["product"]))
	attrs.set("product", pyStr(feature["product"]))
	if w.compliant {
		attrs.set("inference", "COORDINATES:alignment:pilercr:1.02")
		setINSDCDbxrefs(attrs, feature)
		attrs.set("rpt_family", "CRISPR")
		attrs.set("rpt_type", "direct")
		attrs.set("rpt_unit_seq", pyStr(feature["repeat_consensus"]))
		w.write(line, "PILER-CR", "repeat_region", ".", ".", attrs)
		return nil
	}
	w.write(line, "PILER-CR", "CRISPR", ".", ".", attrs)

	repeats := mapSlice(feature["repeats"])
	spacers := mapSlice(feature["spacers"])
	pairs := 0
	for ; pairs < len(repeats) && pairs < len(spacers); pairs++ {
		repeat := &baktaAttrs{}
		repeat.set("ID", fmt.Sprintf("%s_repeat_%d", id, pairs+1))
		repeat.set("Parent", id)
		if err := w.writeCRISPRPart(line.seqID, "crispr-repeat", repeats[pairs], repeat); err != nil {
			return err
		}
		spacer := &baktaAttrs{}
		spacer.set("ID", fmt.Sprintf("%s_spacer_%d", id, pairs+1))
		spacer.set("Parent", id)
		spacer.set("sequence", pyStr(spacers[pairs]["sequence"]))
		if err := w.writeCRISPRPart(line.seqID, "crispr-spacer", spacers[pairs], spacer); err != nil {
			return err
		}
	}
	if len(repeats)-1 == pairs {
		repeat := &baktaAttrs{}
		repeat.set("ID", fmt.Sprintf("%s_repeat_%d", id, pairs+1))
		return w.writeCRISPRPart(line.seqID, "crispr-repeat", repeats[pairs], repeat)
	}
	return nil
}

func (w *currentGFF3Writer) writeCRISPRPart(seqID, featureType string, part map[string]any, attrs *baktaAttrs) error {
	start, startOK := jsonInt(part["start"])
	stop, stopOK := jsonInt(part["stop"])
	if !startOK || !stopOK {
		return fmt.Errorf("feature %s is missing contig, start, or stop", attrs.values["ID"])
	}
	w.writeLine(seqID, "PILER-CR", featureType, strconv.Itoa(start), strconv.Itoa(stop), ".", pyStr(part["strand"]), ".", attrs)
	return nil
}

func (w *currentGFF3Writer) writeCDS(feature map[string]any, line featureLine) {
	locus := featureLocus(feature)
	userProvided := pyStr(feature["source"]) == "user-provided"
	source := "Pyrodigal"
	if userProvided {
		source = "?"
	}
	product := w.proteinProduct(feature)
	attrs := locusAttrs(feature, locus, product)
	attrs.set("product", product)
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	pseudogene, hasPseudogene := cdsPseudogene(feature)
	if hasPseudogene {
		attrs.set("pseudogene", pseudogene)
	} else if _, ok := feature["truncated"]; ok {
		attrs.set("pseudo", "True")
	}
	setIfTruthy(attrs, feature, "gene", "gene")
	if w.compliant {
		attrs.set("Parent", locus+"_gene")
		if userProvided {
			attrs.set("inference", "EXISTENCE:non-experimental evidence, no additional details recorded")
		} else {
			attrs.set("inference", "ab initio prediction:Pyrodigal:3.5")
		}
		setINSDCDbxrefs(attrs, feature)
		setECNumbers(attrs)

		gene := geneAttrs(locus)
		setIfTruthy(gene, feature, "gene", "gene")
		if hasPseudogene {
			gene.set("pseudogene", pseudogene)
		}
		w.writeGene(line, source, gene)
	}
	if exception, ok := feature["exception"].(map[string]any); ok {
		switch pyStr(exception["type"]) {
		case "selenocysteine":
			location := pyStr(exception["start"]) + ".." + pyStr(exception["stop"])
			if line.strand == "-" {
				location = "complement(" + location + ")"
			}
			attrs.set("transl_except", fmt.Sprintf("(pos:%s,aa:%s)", location, pyStr(exception["aa"])))
			note := append(attrs.list("Note"), fmt.Sprintf("codon on position %s is a %s codon",
				pyStr(exception["codon_position"]), pyStr(exception["type"])))
			attrs.setList("Note", note)
		case "ribosomal_slippage":
			attrs.set("ribosomal_slippage", "True")
		}
	}
	w.write(line, source, "CDS", ".", "0", attrs)
	w.writeSignalPeptide(feature, line, locus)
}

func (w *currentGFF3Writer) writeSORF(feature map[string]any, line featureLine) {
	locus := featureLocus(feature)
	product := w.proteinProduct(feature)
	attrs := locusAttrs(feature, locus, product)
	attrs.set("product", product)
	attrs.setList("Dbxref", featureList(feature, "db_xrefs"))
	setIfTruthy(attrs, feature, "gene", "gene")
	if w.compliant {
		attrs.set("Parent", locus+"_gene")
		setINSDCDbxrefs(attrs, feature)
		setECNumbers(attrs)

		gene := geneAttrs(locus)
		gene.set("inference", "ab initio prediction:Bakta")
		setIfTruthy(gene, feature, "gene", "gene")
		w.writeGene(line, "Bakta", gene)
	}
	w.write(line, "Bakta", "CDS", ".", "0", attrs)
	w.writeSignalPeptide(feature, line, locus)
}

// proteinProduct returns a cds or sorf feature's product, revised as Bakta
// --compliant does in compliant mode.
func (w *currentGFF3Writer) proteinProduct(feature map[string]any) string {
	if product, ok := feature["product"].(string); ok && w.compliant {
		return reviseProductINSDC(product)
	}
	return pyStr(feature["product"])
}

func (w *currentGFF3Writer) writeSignalPeptide(feature map[string]any, line featureLine, locus string) {
	peptide, ok := feature["signal-peptide"].(map[string]any)
	if !ok {
		return
	}
	score, _ := jsonFloat(peptide["score"])
	attrs := &baktaAttrs{}
	attrs.set("ID", locus+"_sigpep")
	attrs.set("Name", "signal peptide")
	attrs.set("product", "signal peptide")
	attrs.set("score", pyStr(peptide["score"]))
	attrs.set("Parent", locus)
	w.writeLine(line.seqID, "DeepSig", "signal_peptide", pyStr(peptide["start"]), pyStr(peptide["stop"]),
		strconv.FormatFloat(score, 'f', 2, 64), line.strand, ".", attrs)
}

func (w *currentGFF3Writer) writeGap(feature map[string]any, line featureLine) {
	product := fmt.Sprintf("gap (%s bp)", pyStr(feature["length"]))
	attrs := &baktaAttrs{}
	attrs.set("ID", featureIDValue(feature))
	attrs.set("Name", product)
	attrs.set("product", product)
	w.write(line, "Bakta", "gap", ".", ".", attrs)
}

func (w *currentGFF3Writer) writeOrigin(feature map[string]any, line featureLine, featureType string) {
	gffType := featureType
	if featureType == "oriV" {
		gffType = "oriC"
	}
	if w.compliant && gffType == "oriC" {
		gffType = "rep_origin"
	}
	product := pyStr(feature["product"])
	attrs := &baktaAttrs{}
	attrs.set("ID", featureIDValue(feature))
	attrs.set("Name", product)
	if w.compliant {
		attrs.set("Note", product)
	} else {
		attrs.set("product", product)
		attrs.set("inference", "similar to DNA sequence")
	}
	w.write(line, "BLAST+", gffType, ".", ".", attrs)
}

// featureLocus returns a feature's locus tag. Features without one, which
// Bakta does not write, fall back to their ID.
func featureLocus(feature map[string]any) string {
	if locus, ok := feature["locus"]; ok {
		return pyStr(locus)
	}
	return gffFeatureID(feature)
}

// featureIDValue returns a feature's id, falling back as featureLocus does.
func featureIDValue(feature map[string]any) string {
	if id, ok := feature["id"]; ok {
		return pyStr(id)
	}
	return gffFeatureID(feature)
}

// locusAttrs starts the attributes of a feature with a locus tag.
func locusAttrs(feature map[string]any, locus, name string) *baktaAttrs {
	attrs := &baktaAttrs{}
	attrs.set("ID", locus)
	attrs.set("Name", name)
	attrs.set("locus_tag", locus)
	return attrs
}

// geneAttrs starts the attributes of a compliant mode gene line.
func geneAttrs(locus string) *baktaAttrs {
	attrs := &baktaAttrs{}
	attrs.set("ID", locus+"_gene")
	attrs.set("locus_tag", locus)
	return attrs
}

// rnaGeneAttrs returns the gene line attributes of tmRNA, rRNA and ncRNA
// features.
func rnaGeneAttrs(feature map[string]any, locus string) *baktaAttrs {
	attrs := geneAttrs(locus)
	attrs.set("gene", pyStr(feature["gene"]))
	setTruncatedPseudo(attrs, feature)
	return attrs
}

func setIfTruthy(attrs *baktaAttrs, feature map[string]any, key, field string) {
	if value := feature[field]; pyTruthy(value) {
		attrs.set(key, pyStr(value))
	}
}

func setTruncatedPseudo(attrs *baktaAttrs, feature map[string]any) {
	if _, ok := feature["truncated"]; ok {
		attrs.set("pseudo", "True")
	}
}

// cdsPseudogene returns the pseudogene attribute of a cds feature that has
// pseudogene.
func cdsPseudogene(feature map[string]any) (string, bool) {
	value, ok := feature["pseudogene"]
	if !ok {
		return "", false
	}
	pseudogene, _ := value.(map[string]any)
	if pyTruthy(pseudogene["paralog"]) {
		return "unprocessed", true
	}
	return "unitary", true
}

// featureList returns a feature's list field formatted as values. A missing
// field is an empty list.
func featureList(feature map[string]any, key string) []string {
	raw, _ := feature[key].([]any)
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		values = append(values, pyStr(item))
	}
	return values
}

// dbxrefDatabase returns the text of a db_xref before its first ":".
func dbxrefDatabase(dbxref string) string {
	database, _, _ := strings.Cut(dbxref, ":")
	return database
}

// setINSDCDbxrefs sets Dbxref to the db_xrefs INSDC accepts and Note to the
// others.
func setINSDCDbxrefs(attrs *baktaAttrs, feature map[string]any) {
	var valid, invalid []string
	for _, dbxref := range featureList(feature, "db_xrefs") {
		if insdcDbxrefDatabases[dbxrefDatabase(dbxref)] {
			valid = append(valid, dbxref)
		} else {
			invalid = append(invalid, dbxref)
		}
	}
	attrs.setList("Dbxref", valid)
	attrs.setList("Note", invalid)
}

// setRfamInference sets inference from the last RFAM Dbxref, if any.
func setRfamInference(attrs *baktaAttrs) {
	for _, dbxref := range attrs.list("Dbxref") {
		if dbxrefDatabase(dbxref) != "RFAM" {
			continue
		}
		_, rest, _ := strings.Cut(dbxref, ":")
		accession, _, _ := strings.Cut(rest, ":")
		attrs.set("inference", "profile:Rfam:"+accession)
	}
}

// setECNumbers moves EC entries from Note to ec_number.
func setECNumbers(attrs *baktaAttrs) {
	var notes, ecs []string
	for _, note := range attrs.list("Note") {
		if dbxrefDatabase(note) == "EC" {
			ecs = append(ecs, note)
		} else {
			notes = append(notes, note)
		}
	}
	attrs.setList("Note", notes)
	if len(ecs) != 0 {
		attrs.set("ec_number", strings.ReplaceAll(ecs[len(ecs)-1], "EC:", ""))
	}
}

// classBySOID maps the SO ID of a feature's class, [NAME, SO_ID], to an
// INSDC class, with "other" for anything else.
func classBySOID(feature map[string]any, classes map[string]string) string {
	if class, ok := feature["class"].([]any); ok && len(class) >= 2 {
		if soID, ok := class[1].(string); ok {
			if name, ok := classes[soID]; ok {
				return name
			}
		}
	}
	return "other"
}

// reviseProductINSDC revises a product name as Bakta --compliant does:
// "uncharacterized" becomes "putative", and unbalanced brackets are removed.
func reviseProductINSDC(product string) string {
	product = uncharacterizedPattern.ReplaceAllString(product, "putative")
	if strings.Count(product, "(") != strings.Count(product, ")") {
		product = strings.NewReplacer("(", "", ")", "").Replace(product)
	}
	if strings.Count(product, "[") != strings.Count(product, "]") {
		product = strings.NewReplacer("[", "", "]", "").Replace(product)
	}
	return product
}

// baktaAttrs holds GFF3 attributes in the order each key was first set. A
// value is a string or, for list attributes, a []string.
type baktaAttrs struct {
	keys   []string
	values map[string]any
}

func (a *baktaAttrs) set(key string, value any) {
	if a.values == nil {
		a.values = map[string]any{}
	}
	if _, ok := a.values[key]; !ok {
		a.keys = append(a.keys, key)
	}
	a.values[key] = value
}

func (a *baktaAttrs) setList(key string, values []string) {
	a.set(key, append([]string(nil), values...))
}

// list returns a list attribute's values, or nil if it is unset.
func (a *baktaAttrs) list(key string) []string {
	values, _ := a.values[key].([]string)
	return append([]string(nil), values...)
}

func (a *baktaAttrs) remove(key string) {
	if _, ok := a.values[key]; !ok {
		return
	}
	delete(a.values, key)
	for i, k := range a.keys {
		if k == key {
			a.keys = append(a.keys[:i], a.keys[i+1:]...)
			break
		}
	}
}

func (a *baktaAttrs) String() string {
	parts := make([]string, 0, len(a.keys))
	for _, key := range a.keys {
		switch value := a.values[key].(type) {
		case []string:
			if len(value) == 0 {
				continue
			}
			escaped := make([]string, len(value))
			for i, item := range value {
				escaped[i] = baktaGFF3Escape(item)
			}
			parts = append(parts, key+"="+strings.Join(escaped, ","))
		case string:
			parts = append(parts, key+"="+baktaGFF3Escape(value))
		}
	}
	return strings.Join(parts, ";")
}

var baktaGFF3Escaper = strings.NewReplacer(
	"%", "%25",
	";", "%3B",
	"=", "%3D",
	"&", "%26",
	",", "%2C",
)

// baktaGFF3Escape escapes an attribute value as Bakta does, which leaves
// tabs and newlines as they are.
func baktaGFF3Escape(value string) string {
	return baktaGFF3Escaper.Replace(value)
}

// pyStr formats a decoded JSON value as Python's str() does.
func pyStr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		text := v.String()
		if !strings.ContainsAny(text, ".eE") {
			if i, ok := new(big.Int).SetString(text, 10); ok {
				return i.String()
			}
			return text
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil && !math.IsInf(f, 0) {
			return text
		}
		return pyFloatRepr(f)
	case float64:
		return pyFloatRepr(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprint(v)
	}
}

// pyTruthy reports whether a decoded JSON value is true in Python.
func pyTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	case []any:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	default:
		if f, ok := jsonFloat(value); ok {
			return f != 0
		}
		return true
	}
}

// pyFloatRepr formats f as Python's repr(float): the shortest digits that
// round-trip, in fixed notation for decimal exponents from -4 to 15 and in
// scientific notation otherwise.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	// Go's shortest 'e' format matches Python's scientific notation: no
	// trailing ".0" and at least two exponent digits.
	scientific := strconv.FormatFloat(f, 'e', -1, 64)
	exponent, err := strconv.Atoi(scientific[strings.IndexByte(scientific, 'e')+1:])
	if err != nil || exponent < -4 || exponent >= 16 {
		return scientific
	}
	fixed := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(fixed, ".") {
		fixed += ".0"
	}
	return fixed
}
