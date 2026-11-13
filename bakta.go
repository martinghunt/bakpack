package bakpack

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/martinghunt/faqt/seq"
)

type JSONChecksums struct {
	CanonicalSHA256 string `json:"canonical_sha256"`
}

type ReduceResult struct {
	ReducedJSON []byte
	Original    JSONChecksums
	Reduced     JSONChecksums

	// UnmatchedSequences lists sequences[] IDs whose nucleotides were kept
	// because no genome FASTA record matched them by id or orig_id with
	// identical sequence. Features on these sequences are not reduced either.
	UnmatchedSequences []string

	// reducedRoot is the already-decoded root object backing ReducedJSON, so
	// archive building can reuse it instead of decoding ReducedJSON again.
	reducedRoot map[string]any
}

type RestoreResult struct {
	OriginalJSON []byte
	Original     JSONChecksums
}

const (
	reducedMetadataKey    = "_bakpack"
	reducedMetadataFormat = "bakpack_reduced_bakta_json"
	// Bakta v1.10.0 renamed the nucleotide field of sequences[] entries from
	// "sequence" to "nt", and the sequence ID field of features from "contig"
	// to "sequence". Reduced JSON from the older layout is written as version
	// 1, which older bakpack releases can read. Reduced JSON from the newer
	// layout is written as version 2, so that older bakpack releases reject it
	// with a clear version error instead of restoring it incorrectly.
	reducedMetadataVersionLegacyLayout  = 1
	reducedMetadataVersionCurrentLayout = 2

	legacySequenceNTKey  = "sequence"
	currentSequenceNTKey = "nt"

	// absentStatsMetadata lists derivable stats keys missing from the original
	// JSON, so restore does not add them.
	absentStatsMetadata = "absent_stats"
)

func ReduceBaktaJSON(original []byte, genome Genome) (ReduceResult, error) {
	data, err := decodeBaktaJSONObject(original)
	if err != nil {
		return ReduceResult{}, err
	}
	if _, exists := data[reducedMetadataKey]; exists {
		return ReduceResult{}, fmt.Errorf("Bakta JSON uses reserved top-level key %q", reducedMetadataKey)
	}
	if err := validateBaktaFeatureTypes(data); err != nil {
		return ReduceResult{}, err
	}
	genome = withBaktaContigAliases(data, genome)

	currentLayout := usesCurrentBaktaLayout(data)
	absentStats := absentDerivedStats(data)
	unmatched := stripContigSequences(data, genome, sequenceNTKey(currentLayout))
	table := translationTable(data)
	for _, feature := range baktaFeatures(data) {
		nt, haveNT := FeatureNT(feature, genome)
		if haveNT {
			if existing, ok := feature["nt"].(string); ok && normalizeNT(existing) == nt {
				delete(feature, "nt")
			}
			if existingAA, ok := feature["aa"].(string); ok && aaMatches(nt, existingAA, table) {
				delete(feature, "aa")
			}
		}
	}
	stripDerivableFields(data, genome)

	originalCanonical, err := JSONBytesCanonicalSHA256(original)
	if err != nil {
		return ReduceResult{}, err
	}
	version := reducedMetadataVersionLegacyLayout
	if currentLayout {
		version = reducedMetadataVersionCurrentLayout
	}
	metadata := map[string]any{
		"format":                         reducedMetadataFormat,
		"version":                        version,
		"original_json_canonical_sha256": originalCanonical,
	}
	if len(absentStats) != 0 {
		// reducedRoot feeds the archive encoder, which takes decoded JSON
		// types, so store []any rather than []string.
		keys := make([]any, len(absentStats))
		for i, key := range absentStats {
			keys[i] = key
		}
		metadata[absentStatsMetadata] = keys
	}
	data[reducedMetadataKey] = metadata

	reduced, err := PrettyJSON(data)
	if err != nil {
		return ReduceResult{}, err
	}
	// Restore is the only way to check that everything removed can be
	// re-derived, so catch an unrestorable reduction here rather than at
	// extraction time.
	if _, err := RestoreBaktaJSON(reduced, genome); err != nil {
		return ReduceResult{}, fmt.Errorf("reduced JSON does not restore to the original: %w", err)
	}
	reducedCanonical, err := JSONBytesCanonicalSHA256(reduced)
	if err != nil {
		return ReduceResult{}, err
	}
	return ReduceResult{
		ReducedJSON: reduced,
		Original: JSONChecksums{
			CanonicalSHA256: originalCanonical,
		},
		Reduced: JSONChecksums{
			CanonicalSHA256: reducedCanonical,
		},
		UnmatchedSequences: unmatched,
		reducedRoot:        data,
	}, nil
}

func RestoreBaktaJSON(reduced []byte, genome Genome) (RestoreResult, error) {
	data, err := decodeBaktaJSONObject(reduced)
	if err != nil {
		return RestoreResult{}, err
	}
	metadata, err := reducedMetadata(data)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateBaktaFeatureTypes(data); err != nil {
		return RestoreResult{}, err
	}
	genome = withBaktaContigAliases(data, genome)
	delete(data, reducedMetadataKey)

	restoreDerivableFields(data, genome, metadata.AbsentStats)
	restoreContigSequences(data, genome, sequenceNTKey(metadata.CurrentLayout))
	table := translationTable(data)
	for _, feature := range baktaFeatures(data) {
		info, _ := baktaFeatureTypeString(feature)
		shouldHaveNT := !info.Gap && hasFeatureCoords(feature)
		shouldHaveAA := info.Protein || feature["aa_hexdigest"] != nil
		nt, haveNT := FeatureNT(feature, genome)
		if shouldHaveNT && haveNT {
			if _, exists := feature["nt"]; !exists {
				feature["nt"] = nt
			}
		}
		if shouldHaveAA && haveNT {
			if _, exists := feature["aa"]; !exists {
				aa := translateNT(nt, table, true)
				if digest, ok := feature["aa_hexdigest"].(string); ok && digest != "" {
					if md5Hex([]byte(aa)) != digest {
						// CDSs truncated at a contig edge keep their first
						// codon's literal translation instead of M.
						aa = translateNT(nt, table, false)
						if md5Hex([]byte(aa)) != digest {
							continue
						}
					}
				}
				feature["aa"] = aa
			}
		}
	}

	original, err := PrettyJSON(data)
	if err != nil {
		return RestoreResult{}, err
	}
	canonical, err := JSONBytesCanonicalSHA256(original)
	if err != nil {
		return RestoreResult{}, err
	}
	if metadata.OriginalJSONCanonicalSHA256 != "" && canonical != metadata.OriginalJSONCanonicalSHA256 {
		return RestoreResult{}, fmt.Errorf("original_json_canonical_sha256 mismatch: expected %s, got %s", metadata.OriginalJSONCanonicalSHA256, canonical)
	}
	return RestoreResult{
		OriginalJSON: original,
		Original: JSONChecksums{
			CanonicalSHA256: canonical,
		},
	}, nil
}

func decodeBaktaJSONObject(data []byte) (map[string]any, error) {
	root, err := DecodeJSON(data)
	if err != nil {
		return nil, err
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Bakta JSON root is not an object")
	}
	return obj, nil
}

type reducedJSONMetadata struct {
	OriginalJSONCanonicalSHA256 string
	CurrentLayout               bool
	AbsentStats                 map[string]bool
}

func reducedMetadata(data map[string]any) (reducedJSONMetadata, error) {
	raw, ok := data[reducedMetadataKey]
	if !ok {
		return reducedJSONMetadata{}, fmt.Errorf("reduced JSON is missing %s metadata", reducedMetadataKey)
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return reducedJSONMetadata{}, fmt.Errorf("reduced JSON %s metadata is not an object", reducedMetadataKey)
	}
	format, _ := metadata["format"].(string)
	if format != reducedMetadataFormat {
		return reducedJSONMetadata{}, fmt.Errorf("unsupported reduced JSON metadata format %q", format)
	}
	versionNumber, ok := metadata["version"].(json.Number)
	if !ok {
		return reducedJSONMetadata{}, fmt.Errorf("reduced JSON metadata version is missing or not a number")
	}
	version, err := versionNumber.Int64()
	if err != nil || (version != reducedMetadataVersionLegacyLayout && version != reducedMetadataVersionCurrentLayout) {
		return reducedJSONMetadata{}, fmt.Errorf("unsupported reduced JSON metadata version %s", versionNumber.String())
	}
	canonical, _ := metadata["original_json_canonical_sha256"].(string)
	if canonical == "" {
		return reducedJSONMetadata{}, fmt.Errorf("reduced JSON metadata is missing original_json_canonical_sha256")
	}
	absentStats := map[string]bool{}
	if raw, exists := metadata[absentStatsMetadata]; exists {
		keys, ok := raw.([]any)
		if !ok {
			return reducedJSONMetadata{}, fmt.Errorf("reduced JSON metadata %s is not a list", absentStatsMetadata)
		}
		for _, key := range keys {
			name, ok := key.(string)
			if !ok {
				return reducedJSONMetadata{}, fmt.Errorf("reduced JSON metadata %s contains a non-string value", absentStatsMetadata)
			}
			absentStats[name] = true
		}
	}
	return reducedJSONMetadata{
		OriginalJSONCanonicalSHA256: canonical,
		CurrentLayout:               version == reducedMetadataVersionCurrentLayout,
		AbsentStats:                 absentStats,
	}, nil
}

// stripContigSequences removes sequences[] nucleotides that match the genome
// and returns the IDs of entries whose nucleotides did not match.
func stripContigSequences(data map[string]any, genome Genome, ntKey string) []string {
	var unmatched []string
	for _, entry := range baktaSequences(data) {
		id, _ := entry["id"].(string)
		existing, ok := entry[ntKey].(string)
		if id == "" || !ok {
			continue
		}
		contig, ok := genome.Contig(id)
		if ok && normalizeNT(existing) == string(contig) {
			delete(entry, ntKey)
		} else {
			unmatched = append(unmatched, id)
		}
	}
	return unmatched
}

func restoreContigSequences(data map[string]any, genome Genome, ntKey string) {
	for _, entry := range baktaSequences(data) {
		if _, exists := entry[ntKey]; exists {
			continue
		}
		id, _ := entry["id"].(string)
		contig, ok := genome.Contig(id)
		if id != "" && ok {
			entry[ntKey] = string(contig)
		}
	}
}

// usesCurrentBaktaLayout reports whether data uses the Bakta >= 1.10.0 field
// names: sequences[].nt or a string feature "sequence" ID.
func usesCurrentBaktaLayout(data map[string]any) bool {
	for _, entry := range baktaSequences(data) {
		if _, ok := entry[currentSequenceNTKey].(string); ok {
			return true
		}
	}
	for _, feature := range baktaFeatures(data) {
		if _, ok := feature["sequence"].(string); ok {
			return true
		}
	}
	return false
}

// sequenceNTKey returns the key holding sequences[] nucleotide strings.
func sequenceNTKey(currentLayout bool) string {
	if currentLayout {
		return currentSequenceNTKey
	}
	return legacySequenceNTKey
}

// featureContig returns the ID of the sequence a feature is on, from
// "sequence" (Bakta >= 1.10.0) or "contig" (older Bakta).
func featureContig(feature map[string]any) (string, bool) {
	if id, ok := feature["sequence"].(string); ok {
		return id, true
	}
	id, ok := feature["contig"].(string)
	return id, ok
}

func stripDerivableFields(data map[string]any, genome Genome) {
	stripDerivedStats(data, genome)
	stripSequenceLengths(data, genome)
	stripFeatureDerivableFields(data, genome)
}

func restoreDerivableFields(data map[string]any, genome Genome, absentStats map[string]bool) {
	restoreDerivedStats(data, genome, absentStats)
	restoreSequenceLengths(data, genome)
	restoreFeatureDerivableFields(data, genome)
}

func stripDerivedStats(data map[string]any, genome Genome) {
	statsObj, _ := data["stats"].(map[string]any)
	if statsObj == nil {
		return
	}
	for key, value := range derivedStats(genome) {
		if existing, ok := statsObj[key]; ok && jsonValuesEqual(existing, value) {
			delete(statsObj, key)
		}
	}
}

// absentDerivedStats returns the sorted derivable stats keys missing from a
// stats object, which some Bakta reimplementations write only partially.
func absentDerivedStats(data map[string]any) []string {
	statsObj, _ := data["stats"].(map[string]any)
	if statsObj == nil {
		return nil
	}
	var absent []string
	for key := range derivedStats(Genome{}) {
		if _, ok := statsObj[key]; !ok {
			absent = append(absent, key)
		}
	}
	sort.Strings(absent)
	return absent
}

func restoreDerivedStats(data map[string]any, genome Genome, absentStats map[string]bool) {
	statsObj, _ := data["stats"].(map[string]any)
	if statsObj == nil {
		return
	}
	for key, value := range derivedStats(genome) {
		if _, ok := statsObj[key]; !ok && !absentStats[key] {
			statsObj[key] = value
		}
	}
}

func stripSequenceLengths(data map[string]any, genome Genome) {
	for _, entry := range baktaSequences(data) {
		existing, exists := entry["length"]
		if !exists {
			continue
		}
		id, _ := entry["id"].(string)
		if contig, ok := genome.Contig(id); ok && jsonValuesEqual(existing, len(contig)) {
			delete(entry, "length")
		}
	}
}

func restoreSequenceLengths(data map[string]any, genome Genome) {
	for _, entry := range baktaSequences(data) {
		if _, exists := entry["length"]; exists {
			continue
		}
		id, _ := entry["id"].(string)
		if contig, ok := genome.Contig(id); ok {
			entry["length"] = len(contig)
		}
	}
}

func stripFeatureDerivableFields(data map[string]any, genome Genome) {
	table := translationTable(data)
	for _, feature := range baktaFeatures(data) {
		var nt string
		haveNT := false
		if existing, exists := feature["aa_hexdigest"]; exists {
			if isProteinFeature(feature) {
				nt, haveNT = FeatureNT(feature, genome)
				if haveNT {
					aa := translateNT(nt, table, true)
					if digest, ok := existing.(string); ok && md5Hex([]byte(aa)) == digest {
						delete(feature, "aa_hexdigest")
					}
				}
			}
		}

		if existing, exists := feature["start_type"]; exists {
			info, _ := baktaFeatureTypeString(feature)
			if info.StartCodon {
				if !haveNT {
					nt, haveNT = FeatureNT(feature, genome)
				}
				if haveNT {
					if startType, ok := existing.(string); ok && startType == firstCodon(nt) {
						delete(feature, "start_type")
					}
				}
			}
		}

		if existing, exists := feature["hypothetical"]; exists {
			product, _ := feature["product"].(string)
			if existing == true && product == "hypothetical protein" {
				delete(feature, "hypothetical")
			}
		}

		if existing, exists := feature["length"]; exists {
			info, _ := baktaFeatureTypeString(feature)
			if span, ok := featureSpan(feature, genome); ok && info.Gap && jsonValuesEqual(existing, span) {
				delete(feature, "length")
			}
		}
	}
}

func restoreFeatureDerivableFields(data map[string]any, genome Genome) {
	table := translationTable(data)
	for _, feature := range baktaFeatures(data) {
		var nt string
		haveNT := false
		if _, exists := feature["aa_hexdigest"]; !exists && isProteinFeature(feature) && hasFeatureCoords(feature) {
			nt, haveNT = FeatureNT(feature, genome)
			if haveNT {
				aa := translateNT(nt, table, true)
				feature["aa_hexdigest"] = md5Hex([]byte(aa))
			}
		}

		info, _ := baktaFeatureTypeString(feature)
		if _, exists := feature["start_type"]; !exists && info.StartCodon {
			if !haveNT {
				nt, haveNT = FeatureNT(feature, genome)
			}
			if haveNT {
				feature["start_type"] = firstCodon(nt)
			}
		}

		if _, exists := feature["hypothetical"]; !exists {
			product, _ := feature["product"].(string)
			if product == "hypothetical protein" {
				feature["hypothetical"] = true
			}
		}

		if _, exists := feature["length"]; !exists && info.Gap {
			if span, ok := featureSpan(feature, genome); ok {
				feature["length"] = span
			}
		}
	}
}

func baktaSequences(data map[string]any) []map[string]any {
	return mapSlice(data["sequences"])
}

func baktaFeatures(data map[string]any) []map[string]any {
	return mapSlice(data["features"])
}

func derivedStats(genome Genome) map[string]any {
	lengths := make([]int, 0, len(genome.Contigs))
	totalSize := 0
	nCount := 0
	for _, contig := range genome.Contigs {
		length := len(contig.Seq)
		lengths = append(lengths, length)
		totalSize += length
		nCount += strings.Count(string(contig.Seq), "N")
	}
	nRatio := 0.0
	if totalSize > 0 {
		nRatio = float64(nCount) / float64(totalSize)
	}
	return map[string]any{
		"no_sequences": len(genome.Contigs),
		"size":         totalSize,
		"n_ratio":      nRatio,
		"n50":          n50(lengths),
	}
}

func n50(lengths []int) int {
	total := 0
	for _, length := range lengths {
		total += length
	}
	cumulative := 0
	sorted := append([]int(nil), lengths...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] > sorted[j]
	})
	for _, length := range sorted {
		cumulative += length
		if float64(cumulative) >= float64(total)/2 {
			return length
		}
	}
	return 0
}

func isProteinFeature(feature map[string]any) bool {
	info, ok := baktaFeatureTypeString(feature)
	return ok && info.Protein
}

func baktaFeatureTypeString(feature map[string]any) (baktaFeatureTypeInfo, bool) {
	featureType, _ := feature["type"].(string)
	return baktaFeatureType(featureType)
}

func firstCodon(nt string) string {
	if len(nt) < 3 {
		return strings.ToUpper(nt)
	}
	return strings.ToUpper(nt[:3])
}

func featureSpan(feature map[string]any, genome Genome) (int, bool) {
	start, ok := jsonInt(feature["start"])
	if !ok {
		return 0, false
	}
	stop, ok := jsonInt(feature["stop"])
	if !ok {
		return 0, false
	}
	if start <= stop {
		return stop - start + 1, true
	}
	contigName, _ := featureContig(feature)
	contig, ok := genome.Contig(contigName)
	if !ok || start > len(contig) {
		return 0, false
	}
	return len(contig) - start + 1 + stop, true
}

func translationTable(data map[string]any) int {
	genome, _ := data["genome"].(map[string]any)
	table, ok := jsonInt(genome["translation_table"])
	if !ok || table == 0 {
		return 11
	}
	return table
}

func hasFeatureCoords(feature map[string]any) bool {
	if _, ok := featureContig(feature); !ok {
		return false
	}
	if _, ok := jsonInt(feature["start"]); !ok {
		return false
	}
	if _, ok := jsonInt(feature["stop"]); !ok {
		return false
	}
	return true
}

func aaMatches(nt, expected string, table int) bool {
	expected = strings.ToUpper(expected)
	if translateNT(nt, table, false) == expected {
		return true
	}
	return translateNT(nt, table, true) == expected
}

func translateNT(nt string, table int, forceInitialM bool) string {
	aa := seq.TranslateWithCode([]byte(nt), table)
	if forceInitialM && len(aa) > 0 {
		aa[0] = 'M'
	}
	if len(aa) > 0 && aa[len(aa)-1] == '*' {
		aa = aa[:len(aa)-1]
	}
	return string(aa)
}

func normalizeNT(in string) string {
	return strings.ToUpper(strings.ReplaceAll(in, "U", "T"))
}

func jsonInt(value any) (int, bool) {
	switch v := value.(type) {
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	case float64:
		i := int(v)
		return i, float64(i) == v
	case int:
		return v, true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

func jsonFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

func jsonValuesEqual(left, right any) bool {
	if li, ok := jsonInt(left); ok {
		if ri, ok := jsonInt(right); ok {
			return li == ri
		}
	}
	lf, lok := jsonFloat(left)
	rf, rok := jsonFloat(right)
	if lok && rok {
		return lf == rf
	}
	return left == right
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
