package bakpack

// This file builds the GenBank and EMBL records current Bakta writes for an
// annotation JSON, in its default and --compliant modes, as described in
// docs/bakta-insdc-format.md. faqt's insdc package writes them.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/martinghunt/faqt/insdc"
	"github.com/martinghunt/faqt/seqio"
)

// buildBaktaINSDCRecords returns the GenBank/EMBL records for data, one per
// sequence, dated date.
func buildBaktaINSDCRecords(data map[string]any, compliant bool, date time.Time, warn func(string)) ([]*insdc.Record, error) {
	sequences := mapSlice(data["sequences"])
	featuresByContig, featureOrder := gffFeaturesByContig(data)
	if compliant {
		sequences = compliantSequences(sequences, sequenceIDSet(sequences), featuresByContig, featureOrder, warn)
	}
	b := &baktaINSDCBuilder{compliant: compliant, date: date}
	b.genome, _ = data["genome"].(map[string]any)
	b.version, _ = data["version"].(map[string]any)
	ntKey := sequenceNTKey(usesCurrentBaktaLayout(data))

	records := make([]*insdc.Record, 0, len(sequences))
	for _, sequence := range sequences {
		record, err := b.record(sequence, featuresByContig[pyStr(sequence["id"])], ntKey)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// baktaINSDCBuilder holds what every record of one annotation shares.
type baktaINSDCBuilder struct {
	genome    map[string]any
	version   map[string]any
	compliant bool
	date      time.Time
}

func (b *baktaINSDCBuilder) record(sequence map[string]any, features []map[string]any, ntKey string) (*insdc.Record, error) {
	id := pyStr(sequence["id"])
	nt, _ := sequence[ntKey].(string)
	length, _ := jsonInt(sequence["length"])
	topology, _ := sequence["topology"].(string)
	division := "BCT"
	if pyStr(sequence["type"]) == "contig" {
		division = "HGT"
	}
	source := ""
	if taxon, ok := b.genome["taxon"]; ok {
		source = pyStr(taxon)
	}
	record := &insdc.Record{
		SeqRecord: seqio.SeqRecord{Name: id, Description: b.description(sequence, id), Seq: []byte(nt)},
		ID:        id,
		Annotations: insdc.Annotations{
			MoleculeType:     "DNA",
			Topology:         topology,
			DataFileDivision: division,
			Date:             strings.ToUpper(b.date.Format("02-Jan-2006")),
			Source:           &source,
			Comment:          b.comment(features),
		},
	}
	if taxon := b.genome["taxon"]; pyTruthy(taxon) {
		organism := pyStr(taxon)
		record.Annotations.Organism = &organism
	}

	record.Features = append(record.Features, b.sourceFeature(sequence, length))
	for _, feature := range features {
		built, err := b.features(feature, length)
		if err != nil {
			return nil, err
		}
		record.Features = append(record.Features, built...)
	}
	return record, nil
}

// description returns a record's DEFINITION/DE text.
func (b *baktaINSDCBuilder) description(sequence map[string]any, id string) string {
	description := ""
	if taxon := b.genome["taxon"]; pyTruthy(taxon) {
		description = pyStr(taxon)
	}
	complete := pyTruthy(sequence["complete"])
	switch pyStr(sequence["type"]) {
	case "plasmid":
		name := "unnamed"
		if value, ok := sequence["name"]; ok {
			name = pyStr(value)
		}
		description += " plasmid " + name
		if complete {
			description += ", complete sequence"
		} else {
			description += ", whole genome shotgun sequence"
		}
	case "chromosome":
		if complete {
			description += " chromosome, complete genome"
		} else {
			description += " chromosome " + id + ", whole genome shotgun sequence"
		}
	default:
		description += " " + id + ", whole genome shotgun sequence"
	}
	return strings.TrimPrefix(description, " ")
}

// comment returns a record's comment lines, with the annotation summary of
// its features.
func (b *baktaINSDCBuilder) comment(features []map[string]any) []string {
	counts := map[string]int{}
	pseudogenes := 0
	for _, feature := range features {
		featureType := pyStr(feature["type"])
		counts[featureType]++
		if _, ok := feature["pseudogene"]; ok && featureType == "cds" {
			pseudogenes++
		}
	}
	dbVersion := ""
	if db, ok := b.version["db"].(map[string]any); ok {
		parts := strings.Split(pyStr(db["version"]), ".")
		if len(parts) > 2 {
			parts = parts[:2]
		}
		dbVersion = strings.Join(parts, ".") + ", " + pyStr(db["type"])
	}
	summary := func(label string, value string) string {
		return fmt.Sprintf("%-30s :: %s", label, value)
	}
	count := func(label string, n int) string {
		return summary(label, fmt.Sprintf("%5s", thousands(n)))
	}
	return []string{
		"Annotated with Bakta",
		"Software: v" + pyStr(b.version["bakta"]),
		"Database: v" + dbVersion,
		"DOI: 10.1099/mgen.0.000685",
		"URL: github.com/oschwengers/bakta",
		"",
		"##Genome Annotation Summary:##",
		summary("Annotation Date", b.date.Format("01/02/2006, 15:04:05")),
		count("CDSs", counts["cds"]+counts["sorf"]),
		count("tRNAs", counts["tRNA"]),
		count("tmRNAs", counts["tmRNA"]),
		count("rRNAs", counts["rRNA"]),
		count("ncRNAs", counts["ncRNA"]),
		count("regulatory ncRNAs", counts["ncRNA-region"]),
		count("CRISPR Arrays", counts["crispr"]),
		count("oriCs/oriVs", counts["oriC"]+counts["oriV"]),
		count("oriTs", counts["oriT"]),
		count("gaps", counts["gap"]),
		count("pseudogenes", pseudogenes),
	}
}

// thousands formats n with "," as the thousands separator.
func thousands(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return sign + digits
}

func (b *baktaINSDCBuilder) sourceFeature(sequence map[string]any, length int) insdc.Feature {
	quals := &insdcQualifiers{}
	quals.set("mol_type", "genomic DNA")
	if taxon := b.genome["taxon"]; pyTruthy(taxon) {
		quals.set("organism", taxon)
	}
	if strain := b.genome["strain"]; pyTruthy(strain) {
		quals.set("strain", strain)
	}
	switch pyStr(sequence["type"]) {
	case "plasmid":
		if name := sequence["name"]; pyTruthy(name) {
			quals.set("plasmid", name)
		} else {
			quals.set("plasmid", "unnamed")
		}
	case "chromosome":
		if name := sequence["name"]; pyTruthy(name) {
			quals.set("chromosome", name)
		}
	}
	return insdc.Feature{Type: "source", Location: insdc.Span(0, length, 1), Qualifiers: quals.qualifiers()}
}

// features returns a feature's INSDC features: its gene feature, if it has a
// locus tag, the feature itself, and its signal peptide, if it has one.
func (b *baktaINSDCBuilder) features(feature map[string]any, length int) ([]insdc.Feature, error) {
	featureType, _ := feature["type"].(string)
	start, _ := jsonInt(feature["start"])
	stop, _ := jsonInt(feature["stop"])
	strand := insdcStrand(feature)

	quals := &insdcQualifiers{}
	quals.set("note", []any{})
	if dbxrefs, ok := feature["db_xrefs"]; ok {
		if b.compliant {
			valid, invalid := splitINSDCDbxrefs(featureList(feature, "db_xrefs"))
			quals.set("db_xref", valid)
			quals.set("note", invalid)
		} else {
			quals.set("db_xref", dbxrefs)
		}
	}
	if product, ok := feature["product"]; ok {
		if featureType == "cds" || featureType == "sorf" {
			if _, isString := product.(string); isString {
				product = proteinProduct(feature, b.compliant)
			}
		}
		quals.set("product", product)
	}
	locus, hasLocus := feature["locus"]
	if hasLocus {
		quals.set("locus_tag", locus)
	}

	var insdcType string
	switch featureType {
	case "gap":
		insdcType = "gap"
		quals.set("estimated_length", feature["length"])
	case "oriC", "oriV":
		insdcType = "rep_origin"
		quals.set("inference", "similar to DNA sequence")
		quals.set("note", feature["product"])
		quals.remove("product")
	case "oriT":
		insdcType = "oriT"
		quals.set("inference", "similar to DNA sequence")
		quals.appendTo("note", feature["product"])
		quals.remove("product")
	case "cds", "sorf":
		insdcType = "CDS"
		b.setCDSQualifiers(quals, feature, featureType, start, stop, strand)
	case "tRNA":
		insdcType = "tRNA"
		_, hasAminoAcid := feature["amino_acid"]
		_, hasAntiCodon := feature["anti_codon"]
		if hasAminoAcid && hasAntiCodon {
			if position, ok := feature["anti_codon_pos"].([]any); ok && len(position) == 2 {
				quals.set("anticodon", fmt.Sprintf("(pos:%s..%s,aa:%s,seq:%s)",
					pyStr(position[0]), pyStr(position[1]), pyStr(feature["amino_acid"]), pyStr(feature["anti_codon"])))
			} else {
				quals.appendTo("note", fmt.Sprintf("tRNA-%s (%s)", pyStr(feature["amino_acid"]), pyStr(feature["anti_codon"])))
			}
		}
		quals.set("inference", "profile:tRNAscan:2.0")
		if _, ok := feature["pseudogene"]; ok {
			quals.set("pseudogene", "unknown")
		}
	case "tmRNA":
		insdcType = "tmRNA"
		quals.set("inference", "profile:aragorn:1.2")
		if tag, ok := feature["tag"].(map[string]any); ok {
			quals.set("tag_peptide", pyStr(tag["start"])+".."+pyStr(tag["stop"]))
		}
	case "rRNA":
		insdcType = "rRNA"
		setINSDCRfamInference(quals, feature)
	case "ncRNA":
		insdcType = "ncRNA"
		setINSDCRfamInference(quals, feature)
		quals.set("ncRNA_class", classBySOID(feature, ncRNAClassesBySOID))
	case "ncRNA-region":
		insdcType = "regulatory"
		setINSDCRfamInference(quals, feature)
		quals.set("regulatory_class", classBySOID(feature, regulatoryClassesBySOID))
		quals.appendTo("note", feature["product"])
		quals.remove("product")
	case "crispr":
		insdcType = "repeat_region"
		quals.set("rpt_family", "CRISPR")
		quals.set("rpt_type", "direct")
		quals.set("rpt_unit_seq", feature["repeat_consensus"])
		quals.set("inference", "COORDINATES:alignment:pilercr:1.02")
		quals.appendTo("note", feature["product"])
		quals.remove("product")
	default:
		return nil, unsupportedBaktaFeatureTypeError(featureType)
	}

	location := insdcFeatureLocation(feature, quals, start, stop, strand, length)

	var built []insdc.Feature
	if pyTruthy(locus) {
		gene := &insdcQualifiers{}
		gene.set("locus_tag", locus)
		geneName := feature["gene"]
		keepGene := pyTruthy(geneName)
		if keepGene && b.compliant {
			name, ok := geneName.(string)
			keepGene = ok && insdcNcRNAGenePattern.MatchString(name)
		}
		if keepGene {
			gene.set("gene", geneName)
		}
		if _, ok := feature["pseudogene"]; ok {
			pseudogene := "unknown"
			if featureType == "cds" {
				pseudogene, _ = cdsPseudogene(feature)
			}
			gene.set("pseudogene", pseudogene)
		} else if _, ok := feature["truncated"]; ok {
			gene.set("pseudo", insdcFlag{})
		}
		built = append(built, insdc.Feature{Type: "gene", Location: location, Qualifiers: gene.qualifiers()})
		if keepGene {
			quals.set("gene", geneName)
		}
	}
	built = append(built, insdc.Feature{Type: insdcType, Location: location, Qualifiers: quals.qualifiers()})

	if peptide, ok := feature["signal-peptide"].(map[string]any); ok && (featureType == "cds" || featureType == "sorf") {
		peptideStart, _ := jsonInt(peptide["start"])
		peptideStop, _ := jsonInt(peptide["stop"])
		sigPeptide := &insdcQualifiers{}
		sigPeptide.set("locus_tag", locus)
		sigPeptide.set("inference", "ab initio prediction:DeepSig:1.2")
		built = append(built, insdc.Feature{
			Type:       "sig_peptide",
			Location:   insdc.Span(peptideStart-1, peptideStop, strand),
			Qualifiers: sigPeptide.qualifiers(),
		})
	}
	return built, nil
}

// setCDSQualifiers sets the type-specific qualifiers of cds and sorf features.
func (b *baktaINSDCBuilder) setCDSQualifiers(quals *insdcQualifiers, feature map[string]any, featureType string, start, stop, strand int) {
	if pseudogene, ok := cdsPseudogene(feature); ok {
		quals.set("pseudogene", pseudogene)
		details, _ := feature["pseudogene"].(map[string]any)
		quals.appendTo("note", details["description"])
	} else {
		quals.set("protein_id", "gnl|Bakta|"+pyStr(feature["locus"]))
		quals.set("translation", feature["aa"])
	}
	quals.set("codon_start", 1)
	quals.set("transl_table", b.genome["translation_table"])

	var inference []any
	switch {
	case featureType == "sorf":
		parts := strings.Split(pyStr(b.version["bakta"]), ".")
		if len(parts) > 2 {
			parts = parts[:2]
		}
		inference = append(inference, "ab initio prediction:Bakta:"+strings.Join(parts, "."))
	case pyStr(feature["source"]) == "user-provided":
		inference = append(inference, "EXISTENCE:non-experimental evidence, no additional details recorded")
	default:
		inference = append(inference, "ab initio prediction:Prodigal:2.6")
	}
	if similar, ok := cdsSimilarity(feature); ok {
		inference = append(inference, similar)
	}
	quals.set("inference", inference)

	if b.compliant {
		notes := make([]string, 0, len(quals.values["note"]))
		for _, note := range quals.values["note"] {
			notes = append(notes, pyStr(note))
		}
		rest, ec, ok := splitECNumbers(notes)
		quals.set("note", rest)
		if ok {
			quals.set("EC_number", ec)
		}
	}

	if exception, ok := feature["exception"].(map[string]any); ok {
		switch pyStr(exception["type"]) {
		case "selenocysteine":
			location := pyStr(exception["start"]) + ".." + pyStr(exception["stop"])
			if pyStr(feature["strand"]) == "-" {
				location = "complement(" + location + ")"
			}
			quals.set("transl_except", fmt.Sprintf("(pos:%s,aa:%s)", location, pyStr(exception["aa"])))
			quals.appendTo("note", fmt.Sprintf("codon on position %s is a selenocysteine codon", pyStr(exception["codon_position"])))
		case "ribosomal_slippage":
			quals.set("ribosomal_slippage", insdcFlag{})
		}
	}
}

// cdsSimilarity returns the inference of a cds or sorf feature from its best
// protein database hit, if any.
func cdsSimilarity(feature map[string]any) (string, bool) {
	ups, _ := feature["ups"].(map[string]any)
	ips, _ := feature["ips"].(map[string]any)
	psc, _ := feature["psc"].(map[string]any)
	pscc, _ := feature["pscc"].(map[string]any)
	if id, ok := ups["ncbi_nrp_id"]; ok {
		return "similar to AA sequence:RefSeq:" + pyStr(id), true
	}
	if id, ok := ips["uniref100_id"]; ok {
		return "similar to AA sequence:UniRef:" + pyStr(id), true
	}
	if id, ok := ups["uniparc_id"]; ok {
		return "similar to AA sequence:UniParc:" + pyStr(id), true
	}
	if id, ok := psc["uniref90_id"]; ok && pyTruthy(psc["valid"]) {
		return "similar to AA sequence:UniRef:" + pyStr(id), true
	}
	if id, ok := pscc["uniref50_id"]; ok {
		return "similar to AA sequence:UniRef:" + pyStr(id), true
	}
	return "", false
}

// setINSDCRfamInference sets inference from the last RFAM entry of a
// feature's db_xrefs, if any.
func setINSDCRfamInference(quals *insdcQualifiers, feature map[string]any) {
	if inference, ok := rfamInference(featureList(feature, "db_xrefs")); ok {
		quals.set("inference", inference)
	}
}

// insdcStrand returns the location strand of a feature's strand.
func insdcStrand(feature map[string]any) int {
	switch pyStr(feature["strand"]) {
	case "+":
		return 1
	case "-":
		return -1
	}
	return 0
}

// insdcFeatureLocation returns a feature's location, setting the qualifiers
// that come with a truncated location.
func insdcFeatureLocation(feature map[string]any, quals *insdcQualifiers, start, stop, strand, length int) insdc.Location {
	if _, ok := feature["edge"]; ok {
		parts := []insdc.Location{insdc.Span(start-1, length, strand), insdc.Span(0, stop, strand)}
		if strand == -1 {
			parts[0], parts[1] = parts[1], parts[0]
		}
		return insdc.Location{Parts: parts, Operator: "join"}
	}
	location := insdc.Span(start-1, stop, strand)
	truncated, ok := feature["truncated"]
	if !ok {
		return location
	}
	if _, ok := feature["pseudogene"]; !ok {
		quals.set("pseudo", insdcFlag{})
	}
	switch pyStr(truncated) {
	case "5-prime":
		quals.appendTo("note", "(5' truncated)")
		if strand == 1 {
			location.Start.Kind = insdc.Before
		} else {
			location.End.Kind = insdc.After
		}
	case "3-prime":
		quals.appendTo("note", "(3' truncated)")
		if strand == 1 {
			location.End.Kind = insdc.After
		} else {
			location.Start.Kind = insdc.Before
		}
	case "both":
		quals.appendTo("note", "(partial)")
		location.Start.Kind = insdc.Before
		location.End.Kind = insdc.After
	}
	return location
}

// insdcFlag is a value-less qualifier value, such as /pseudo.
type insdcFlag struct{}

// insdcQualifiers holds a feature's qualifiers in the order each key was
// first set. Each value is a decoded JSON value, a Go string or int, or an
// insdcFlag; a qualifier set to a list holds its items.
type insdcQualifiers struct {
	keys   []string
	values map[string][]any
}

func (q *insdcQualifiers) set(key string, value any) {
	if q.values == nil {
		q.values = map[string][]any{}
	}
	if _, ok := q.values[key]; !ok {
		q.keys = append(q.keys, key)
	}
	switch v := value.(type) {
	case []any:
		q.values[key] = append([]any{}, v...)
	case []string:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = item
		}
		q.values[key] = items
	default:
		q.values[key] = []any{value}
	}
}

// appendTo appends value to the list qualifier key.
func (q *insdcQualifiers) appendTo(key string, value any) {
	q.set(key, append(q.values[key], value))
}

func (q *insdcQualifiers) remove(key string) {
	if _, ok := q.values[key]; !ok {
		return
	}
	delete(q.values, key)
	for i, k := range q.keys {
		if k == key {
			q.keys = append(q.keys[:i], q.keys[i+1:]...)
			break
		}
	}
}

func (q *insdcQualifiers) qualifiers() []insdc.Qualifier {
	qualifiers := make([]insdc.Qualifier, 0, len(q.keys))
	for _, key := range q.keys {
		values := make([]insdc.Value, len(q.values[key]))
		for i, value := range q.values[key] {
			values[i] = insdcValue(value)
		}
		qualifiers = append(qualifiers, insdc.Qualifier{Key: key, Values: values})
	}
	return qualifiers
}

// insdcValue returns a qualifier value as Python's Biopython writes it:
// integers and bools unquoted, anything else as text.
func insdcValue(value any) insdc.Value {
	switch v := value.(type) {
	case insdcFlag:
		return insdc.Flag()
	case int:
		return insdc.Int(strconv.Itoa(v))
	case bool:
		return insdc.Int(pyStr(v))
	case json.Number:
		if !strings.ContainsAny(v.String(), ".eE") {
			return insdc.Int(pyStr(v))
		}
	}
	return insdc.Text(pyStr(value))
}
