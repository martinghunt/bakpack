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

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
