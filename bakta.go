package bakpack

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/martinghunt/faqt/seq"
)

type JSONChecksums struct {
	BytesSHA256     string `json:"bytes_sha256"`
	CanonicalSHA256 string `json:"canonical_sha256"`
}

type ReduceResult struct {
	ReducedJSON []byte
	Original    JSONChecksums
	Reduced     JSONChecksums
}

type RestoreResult struct {
	OriginalJSON []byte
	Original     JSONChecksums
}

func ReduceBaktaJSON(original []byte, genome Genome) (ReduceResult, error) {
	root, err := DecodeJSON(original)
	if err != nil {
		return ReduceResult{}, err
	}
	data, ok := root.(map[string]any)
	if !ok {
		return ReduceResult{}, fmt.Errorf("Bakta JSON root is not an object")
	}

	stripContigSequences(data, genome)
	table := translationTable(data)
	features, _ := data["features"].([]any)
	for _, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			continue
		}
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

	reduced, err := PrettyJSON(data)
	if err != nil {
		return ReduceResult{}, err
	}
	originalCanonical, err := JSONBytesCanonicalSHA256(original)
	if err != nil {
		return ReduceResult{}, err
	}
	reducedCanonical, err := JSONBytesCanonicalSHA256(reduced)
	if err != nil {
		return ReduceResult{}, err
	}
	return ReduceResult{
		ReducedJSON: reduced,
		Original: JSONChecksums{
			BytesSHA256:     SHA256Hex(original),
			CanonicalSHA256: originalCanonical,
		},
		Reduced: JSONChecksums{
			BytesSHA256:     SHA256Hex(reduced),
			CanonicalSHA256: reducedCanonical,
		},
	}, nil
}

func RestoreBaktaJSON(reduced []byte, genome Genome) (RestoreResult, error) {
	root, err := DecodeJSON(reduced)
	if err != nil {
		return RestoreResult{}, err
	}
	data, ok := root.(map[string]any)
	if !ok {
		return RestoreResult{}, fmt.Errorf("Bakta JSON root is not an object")
	}

	restoreDerivableFields(data, genome)
	restoreContigSequences(data, genome)
	table := translationTable(data)
	features, _ := data["features"].([]any)
	for _, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			continue
		}
		featureType, _ := feature["type"].(string)
		shouldHaveNT := featureType != "gap" && hasFeatureCoords(feature)
		shouldHaveAA := featureType == "cds" || featureType == "sorf" || feature["aa_hexdigest"] != nil
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
						continue
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
	return RestoreResult{
		OriginalJSON: original,
		Original: JSONChecksums{
			BytesSHA256:     SHA256Hex(original),
			CanonicalSHA256: canonical,
		},
	}, nil
}

func stripContigSequences(data map[string]any, genome Genome) {
	sequences, _ := data["sequences"].([]any)
	for _, item := range sequences {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		existing, ok := entry["sequence"].(string)
		if id == "" || !ok {
			continue
		}
		contig, ok := genome.Contig(id)
		if ok && normalizeNT(existing) == string(contig) {
			delete(entry, "sequence")
		}
	}
}

func restoreContigSequences(data map[string]any, genome Genome) {
	sequences, _ := data["sequences"].([]any)
	for _, item := range sequences {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if _, exists := entry["sequence"]; exists {
			continue
		}
		id, _ := entry["id"].(string)
		contig, ok := genome.Contig(id)
		if id != "" && ok {
			entry["sequence"] = string(contig)
		}
	}
}

func stripDerivableFields(data map[string]any, genome Genome) {
	stripDerivedStats(data, genome)
	stripSequenceLengths(data, genome)
	stripFeatureDerivableFields(data, genome)
}

func restoreDerivableFields(data map[string]any, genome Genome) {
	restoreDerivedStats(data, genome)
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

func restoreDerivedStats(data map[string]any, genome Genome) {
	statsObj, _ := data["stats"].(map[string]any)
	if statsObj == nil {
		return
	}
	for key, value := range derivedStats(genome) {
		if _, ok := statsObj[key]; !ok {
			statsObj[key] = value
		}
	}
}

func stripSequenceLengths(data map[string]any, genome Genome) {
	sequences, _ := data["sequences"].([]any)
	for _, item := range sequences {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
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
	sequences, _ := data["sequences"].([]any)
	for _, item := range sequences {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
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
	features, _ := data["features"].([]any)
	for _, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			continue
		}

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
			featureType, _ := feature["type"].(string)
			if featureType == "cds" {
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
			featureType, _ := feature["type"].(string)
			if span, ok := featureSpan(feature, genome); ok && featureType == "gap" && jsonValuesEqual(existing, span) {
				delete(feature, "length")
			}
		}
	}
}

func restoreFeatureDerivableFields(data map[string]any, genome Genome) {
	table := translationTable(data)
	features, _ := data["features"].([]any)
	for _, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			continue
		}

		var nt string
		haveNT := false
		if _, exists := feature["aa_hexdigest"]; !exists && isProteinFeature(feature) && hasFeatureCoords(feature) {
			nt, haveNT = FeatureNT(feature, genome)
			if haveNT {
				aa := translateNT(nt, table, true)
				feature["aa_hexdigest"] = md5Hex([]byte(aa))
			}
		}

		featureType, _ := feature["type"].(string)
		if _, exists := feature["start_type"]; !exists && featureType == "cds" {
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

		if _, exists := feature["length"]; !exists && featureType == "gap" {
			if span, ok := featureSpan(feature, genome); ok {
				feature["length"] = span
			}
		}
	}
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
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] > sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, length := range sorted {
		cumulative += length
		if float64(cumulative) >= float64(total)/2 {
			return length
		}
	}
	return 0
}

func isProteinFeature(feature map[string]any) bool {
	featureType, _ := feature["type"].(string)
	return featureType == "cds" || featureType == "sorf"
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
	contigName, _ := feature["contig"].(string)
	contig, ok := genome.Contig(contigName)
	if !ok {
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
	if _, ok := feature["contig"].(string); !ok {
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
