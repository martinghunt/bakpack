package bakpack

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type optimizedCodecBuilder struct {
	valueSchemaSet   map[string][]string
	featureSchemaSet map[string][]string
	fieldSet         map[string]bool
	stats            map[string]*fieldStats
	topKeys          []string
}

type fieldStats struct {
	count           int
	types           map[string]int
	scalarSeen      map[string]bool
	scalarValues    []any
	listElemTypes   map[string]int
	minInt          *int64
	maxInt          *int64
	prefixCandidate bool
}

func newOptimizedCodecBuilder() *optimizedCodecBuilder {
	return &optimizedCodecBuilder{
		valueSchemaSet:   map[string][]string{},
		featureSchemaSet: map[string][]string{},
		fieldSet:         map[string]bool{},
		stats:            map[string]*fieldStats{},
	}
}

func (b *optimizedCodecBuilder) observeReducedJSON(sampleID string, reduced []byte) error {
	root, err := DecodeJSON(reduced)
	if err != nil {
		return fmt.Errorf("%s: decode reduced JSON: %w", sampleID, err)
	}
	data, ok := root.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: reduced JSON root is not an object", sampleID)
	}
	if err := validateBaktaFeatureTypes(data); err != nil {
		return fmt.Errorf("%s: %w", sampleID, err)
	}
	keys := sortedObjectKeys(data)
	if b.topKeys == nil {
		b.topKeys = keys
	} else if !sameStrings(b.topKeys, keys) {
		return fmt.Errorf("%s: top-level JSON keys differ from first sample", sampleID)
	}

	collectValueSchemas(data, b.valueSchemaSet)
	metadata := makeMetadataRecord(data)
	collectValueSchemas(metadata, b.valueSchemaSet)

	features, ok := data["features"].([]any)
	if !ok {
		return fmt.Errorf("%s: reduced JSON has no features array", sampleID)
	}
	valuesByField := map[string][]any{}
	for _, item := range features {
		feature, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: feature is not an object", sampleID)
		}
		featureKeys := sortedObjectKeys(feature)
		b.featureSchemaSet[schemaKey(featureKeys)] = featureKeys
		for _, key := range featureKeys {
			value := feature[key]
			b.fieldSet[key] = true
			statsForField := b.stats[key]
			if statsForField == nil {
				statsForField = newFieldStats()
				b.stats[key] = statsForField
			}
			statsForField.add(value)
			valuesByField[key] = append(valuesByField[key], value)
		}
	}
	for field, values := range valuesByField {
		b.stats[field].addSampleValues(field, values)
	}
	return nil
}

func (b *optimizedCodecBuilder) finish() (*optimizedArchiveCodec, error) {
	if b.topKeys == nil {
		return nil, fmt.Errorf("cannot build archive with no samples")
	}
	valueSchemas := makeSchemaEntries(b.valueSchemaSet)
	featureSchemas := makeSchemaEntries(b.featureSchemaSet)
	featureFields := make([]string, 0, len(b.fieldSet))
	for field := range b.fieldSet {
		featureFields = append(featureFields, field)
	}
	sort.Strings(featureFields)

	fieldCodecs := make([]FieldCodec, len(featureFields))
	for i, field := range featureFields {
		fieldCodecs[i] = chooseFieldCodec(field, b.stats[field])
	}

	codec := &optimizedArchiveCodec{
		TopKeys:        b.topKeys,
		ValueSchemas:   valueSchemas,
		FeatureSchemas: featureSchemas,
		FeatureFields:  featureFields,
		FieldCodecs:    fieldCodecs,
	}
	codec.rebuildLookupMaps()
	return codec, nil
}

func newFieldStats() *fieldStats {
	return &fieldStats{
		types:           map[string]int{},
		scalarSeen:      map[string]bool{},
		listElemTypes:   map[string]int{},
		prefixCandidate: true,
	}
}

func (s *fieldStats) add(value any) {
	s.count++
	typeName := jsonValueType(value)
	s.types[typeName]++
	if scalar, ok := normalizedScalar(value); ok {
		key := scalarKey(scalar)
		if !s.scalarSeen[key] {
			s.scalarSeen[key] = true
			s.scalarValues = append(s.scalarValues, scalar)
		}
	}
	if typeName == "int" {
		number, _ := jsonInt64(value)
		if s.minInt == nil || number < *s.minInt {
			v := number
			s.minInt = &v
		}
		if s.maxInt == nil || number > *s.maxInt {
			v := number
			s.maxInt = &v
		}
	}
	if list, ok := value.([]any); ok {
		elemTypes := map[string]bool{}
		for _, item := range list {
			elemTypes[jsonValueType(item)] = true
		}
		parts := make([]string, 0, len(elemTypes))
		for part := range elemTypes {
			parts = append(parts, part)
		}
		sort.Strings(parts)
		s.listElemTypes[strings.Join(parts, ",")]++
	}
}

func (s *fieldStats) addSampleValues(field string, values []any) {
	if field != "id" && field != "locus" {
		return
	}
	if !s.prefixCandidate {
		return
	}
	_, _, _, err := parseSamplePrefixValues(values)
	if err != nil {
		s.prefixCandidate = false
	}
}

func chooseFieldCodec(field string, stats *fieldStats) FieldCodec {
	if field == "contig" {
		return FieldCodec{Field: field, Kind: fieldCodecSequenceIndex}
	}
	if (field == "id" || field == "locus") && stats.prefixCandidate {
		return FieldCodec{Field: field, Kind: fieldCodecSamplePrefixUintString}
	}
	if onlyType(stats.types, "null") {
		return FieldCodec{Field: field, Kind: fieldCodecConstNull}
	}
	if onlyType(stats.types, "bool") {
		if len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: fieldCodecConstBool, Value: stats.scalarValues[0]}
		}
		return FieldCodec{Field: field, Kind: fieldCodecBoolBitset}
	}
	if onlyType(stats.types, "int") {
		if stats.minInt != nil && *stats.minInt >= 0 {
			return FieldCodec{Field: field, Kind: fieldCodecUint}
		}
		return FieldCodec{Field: field, Kind: fieldCodecInt}
	}
	if onlyType(stats.types, "float") {
		return FieldCodec{Field: field, Kind: fieldCodecRawNumber}
	}
	if onlyType(stats.types, "string") {
		if len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: fieldCodecConstString, Value: stats.scalarValues[0]}
		}
		if len(stats.scalarValues) <= 256 {
			return FieldCodec{Field: field, Kind: fieldCodecEnumString, Values: scalarStrings(stats.scalarValues)}
		}
		return FieldCodec{Field: field, Kind: fieldCodecRawString}
	}
	if typeSubset(stats.types, "string", "null") {
		stringsOnly := scalarStringsWithoutNull(stats.scalarValues)
		if len(stringsOnly) == 0 {
			return FieldCodec{Field: field, Kind: fieldCodecConstNull}
		}
		if len(stringsOnly) == 1 && len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: fieldCodecConstString, Value: stringsOnly[0]}
		}
		if len(stringsOnly) <= 256 {
			return FieldCodec{Field: field, Kind: fieldCodecNullableEnumString, Values: stringsOnly}
		}
		return FieldCodec{Field: field, Kind: fieldCodecNullableRawString}
	}
	return FieldCodec{Field: field, Kind: fieldCodecGeneric}
}

func collectValueSchemas(value any, schemas map[string][]string) {
	switch v := value.(type) {
	case map[string]any:
		keys := sortedObjectKeys(v)
		schemas[schemaKey(keys)] = keys
		for _, key := range keys {
			collectValueSchemas(v[key], schemas)
		}
	case []any:
		for _, item := range v {
			collectValueSchemas(item, schemas)
		}
	}
}

func makeMetadataRecord(data map[string]any) map[string]any {
	metadata := map[string]any{}
	for key, value := range data {
		if key != "features" {
			metadata[key] = value
		}
	}
	return metadata
}

func makeSchemaEntries(schemaSet map[string][]string) []SchemaIndexEntry {
	keys := make([]string, 0, len(schemaSet))
	for key := range schemaSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]SchemaIndexEntry, len(keys))
	for i, key := range keys {
		entries[i] = SchemaIndexEntry{SchemaID: i, Keys: schemaSet[key]}
	}
	return entries
}

func sortedObjectKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func schemaKey(keys []string) string {
	data, _ := json.Marshal(keys)
	return string(data)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func onlyType(types map[string]int, want string) bool {
	return len(types) == 1 && types[want] > 0
}

func typeSubset(types map[string]int, allowed ...string) bool {
	allowedSet := map[string]bool{}
	for _, value := range allowed {
		allowedSet[value] = true
	}
	for value := range types {
		if !allowedSet[value] {
			return false
		}
	}
	return true
}

func jsonValueType(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number:
		if _, ok := jsonNumberInt64(v); ok {
			return "int"
		}
		return "float"
	case int, int64:
		return "int"
	case float64:
		return "float"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func normalizedScalar(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case bool:
		return v, true
	case string:
		return v, true
	case json.Number:
		if number, ok := jsonNumberInt64(v); ok {
			return number, true
		}
		float, err := v.Float64()
		return float, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return v, true
	default:
		return nil, false
	}
}

func scalarKey(value any) string {
	return fmt.Sprintf("%T:%v", value, value)
}

func scalarStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if ok {
			out = append(out, text)
		}
	}
	return out
}

func scalarStringsWithoutNull(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if ok {
			out = append(out, text)
		}
	}
	return out
}
