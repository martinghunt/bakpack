package bakpack

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	optimizedPayloadFormat = "specialized_columnar_chunklocal_v9"
	optimizedChunkMagic    = "BSC8"

	valueTagNull   = 0
	valueTagFalse  = 1
	valueTagTrue   = 2
	valueTagInt    = 3
	valueTagFloat  = 4
	valueTagString = 5
	valueTagList   = 6
	valueTagObject = 7
	valueTagNumber = 8
)

type SchemaIndexEntry struct {
	SchemaID int      `json:"schema_id"`
	Keys     []string `json:"keys"`
}

type FieldCodec struct {
	Field  string   `json:"field"`
	Kind   string   `json:"kind"`
	Value  any      `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

type optimizedArchiveCodec struct {
	TopKeys        []string
	ValueSchemas   []SchemaIndexEntry
	FeatureSchemas []SchemaIndexEntry
	FeatureFields  []string
	FieldCodecs    []FieldCodec

	valueSchemaIDs   map[string]int
	featureSchemaIDs map[string]int
	fieldIDs         map[string]int
}

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

type optimizedSampleDirectory struct {
	sampleID     string
	filename     string
	featureCount int
	metaOffset   int
	metaLength   int
	schemaOffset int
	schemaLength int
	fieldOffsets []int
	fieldLengths []int
}

type optimizedStreamInfo struct {
	metaStart   int
	schemaStart int
	fieldStarts []int
}

func newOptimizedArchiveCodec(packed []packedSampleForArchive) (*optimizedArchiveCodec, error) {
	if len(packed) == 0 {
		return nil, fmt.Errorf("cannot build archive with no samples")
	}

	builder := newOptimizedCodecBuilder()
	for i := range packed {
		if err := builder.observeReducedJSON(packed[i].index.SampleID, packed[i].reduced); err != nil {
			return nil, err
		}
	}
	return builder.finish()
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

func optimizedCodecFromChunk(chunk ChunkIndex) (*optimizedArchiveCodec, error) {
	return optimizedCodecFromMetadata(
		chunk.TopKeys,
		chunk.ValueSchemas,
		chunk.FeatureSchemas,
		chunk.FeatureFields,
		chunk.FieldCodecs,
	)
}

func optimizedCodecFromMetadata(topKeys []string, valueSchemas []SchemaIndexEntry, featureSchemas []SchemaIndexEntry, featureFields []string, fieldCodecs []FieldCodec) (*optimizedArchiveCodec, error) {
	codec := &optimizedArchiveCodec{
		TopKeys:        append([]string(nil), topKeys...),
		ValueSchemas:   append([]SchemaIndexEntry(nil), valueSchemas...),
		FeatureSchemas: append([]SchemaIndexEntry(nil), featureSchemas...),
		FeatureFields:  append([]string(nil), featureFields...),
		FieldCodecs:    append([]FieldCodec(nil), fieldCodecs...),
	}
	if len(codec.ValueSchemas) == 0 || len(codec.FeatureFields) != len(codec.FieldCodecs) {
		return nil, fmt.Errorf("archive index is missing optimized codec metadata")
	}
	codec.rebuildLookupMaps()
	return codec, nil
}

func (c *optimizedArchiveCodec) rebuildLookupMaps() {
	c.valueSchemaIDs = map[string]int{}
	for _, entry := range c.ValueSchemas {
		c.valueSchemaIDs[schemaKey(entry.Keys)] = entry.SchemaID
	}
	c.featureSchemaIDs = map[string]int{}
	for _, entry := range c.FeatureSchemas {
		c.featureSchemaIDs[schemaKey(entry.Keys)] = entry.SchemaID
	}
	c.fieldIDs = map[string]int{}
	for i, field := range c.FeatureFields {
		c.fieldIDs[field] = i
	}
}

func (c *optimizedArchiveCodec) encodeChunk(chunkID int, packed []packedSampleForArchive) ([]byte, []SampleIndex, error) {
	nFields := len(c.FeatureFields)
	var metaStream bytes.Buffer
	var schemaStream bytes.Buffer
	fieldStreams := make([]bytes.Buffer, nFields)
	dirs := make([]optimizedSampleDirectory, 0, len(packed))
	sampleIndexes := make([]SampleIndex, 0, len(packed))

	for _, sample := range packed {
		data := sample.reducedRoot
		if data == nil {
			root, err := DecodeJSON(sample.reduced)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: decode reduced JSON: %w", sample.index.SampleID, err)
			}
			var ok bool
			data, ok = root.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s: reduced JSON root is not an object", sample.index.SampleID)
			}
		}
		metadata := makeMetadataRecord(data)
		features, ok := data["features"].([]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: reduced JSON has no features array", sample.index.SampleID)
		}

		metaOffset := metaStream.Len()
		if err := c.encodeValue(&metaStream, metadata); err != nil {
			return nil, nil, fmt.Errorf("%s: encode metadata: %w", sample.index.SampleID, err)
		}
		metaLength := metaStream.Len() - metaOffset

		schemaOffset := schemaStream.Len()
		fieldOffsets := make([]int, nFields)
		for i := range fieldStreams {
			fieldOffsets[i] = fieldStreams[i].Len()
		}
		valuesByField := make([][]any, nFields)

		for _, item := range features {
			feature, ok := item.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("%s: feature is not an object", sample.index.SampleID)
			}
			keys := sortedObjectKeys(feature)
			schemaID, ok := c.featureSchemaIDs[schemaKey(keys)]
			if !ok {
				return nil, nil, fmt.Errorf("%s: unknown feature schema %v", sample.index.SampleID, keys)
			}
			writeUvarint(&schemaStream, uint64(schemaID))
			for _, key := range keys {
				fieldID, ok := c.fieldIDs[key]
				if !ok {
					return nil, nil, fmt.Errorf("%s: unknown feature field %q", sample.index.SampleID, key)
				}
				valuesByField[fieldID] = append(valuesByField[fieldID], feature[key])
			}
		}
		schemaLength := schemaStream.Len() - schemaOffset

		for fieldID, values := range valuesByField {
			encoded, err := c.encodeFieldValues(c.FieldCodecs[fieldID], values, metadata)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: encode field %q: %w", sample.index.SampleID, c.FeatureFields[fieldID], err)
			}
			fieldStreams[fieldID].Write(encoded)
		}

		fieldLengths := make([]int, nFields)
		for i := range fieldStreams {
			fieldLengths[i] = fieldStreams[i].Len() - fieldOffsets[i]
		}
		dirs = append(dirs, optimizedSampleDirectory{
			sampleID:     sample.index.SampleID,
			filename:     sample.index.AnnotationName,
			featureCount: len(features),
			metaOffset:   metaOffset,
			metaLength:   metaLength,
			schemaOffset: schemaOffset,
			schemaLength: schemaLength,
			fieldOffsets: fieldOffsets,
			fieldLengths: fieldLengths,
		})

		entry := sample.index
		entry.ChunkID = chunkID
		sampleIndexes = append(sampleIndexes, entry)
	}

	var out bytes.Buffer
	out.WriteString(optimizedChunkMagic)
	writeUvarint(&out, uint64(len(dirs)))
	writeUvarint(&out, uint64(nFields))
	writeUvarint(&out, uint64(metaStream.Len()))
	writeUvarint(&out, uint64(schemaStream.Len()))
	for i := range fieldStreams {
		writeUvarint(&out, uint64(fieldStreams[i].Len()))
	}
	for _, dir := range dirs {
		writeString(&out, dir.sampleID)
		writeString(&out, dir.filename)
		writeUvarint(&out, uint64(dir.featureCount))
		writeUvarint(&out, uint64(dir.metaOffset))
		writeUvarint(&out, uint64(dir.metaLength))
		writeUvarint(&out, uint64(dir.schemaOffset))
		writeUvarint(&out, uint64(dir.schemaLength))
		for i := 0; i < nFields; i++ {
			writeUvarint(&out, uint64(dir.fieldOffsets[i]))
			writeUvarint(&out, uint64(dir.fieldLengths[i]))
		}
	}
	out.Write(metaStream.Bytes())
	out.Write(schemaStream.Bytes())
	for i := range fieldStreams {
		out.Write(fieldStreams[i].Bytes())
	}
	return out.Bytes(), sampleIndexes, nil
}

func (c *optimizedArchiveCodec) decodeChunk(chunkBytes []byte, wanted []string) (map[string][]byte, error) {
	dirs, streams, err := parseOptimizedChunkDirectory(chunkBytes, len(c.FeatureFields))
	if err != nil {
		return nil, err
	}
	wantedSet := map[string]bool{}
	if len(wanted) > 0 {
		for _, sample := range wanted {
			wantedSet[sample] = true
		}
	}
	out := map[string][]byte{}
	for _, dir := range dirs {
		if len(wantedSet) > 0 && !wantedSet[dir.sampleID] {
			continue
		}
		value, err := c.decodeSample(chunkBytes, dir, streams)
		if err != nil {
			return nil, fmt.Errorf("%s: decode optimized chunk sample: %w", dir.sampleID, err)
		}
		data, err := PrettyJSON(value)
		if err != nil {
			return nil, err
		}
		out[dir.sampleID] = data
	}
	for sample := range wantedSet {
		if _, ok := out[sample]; !ok {
			return nil, fmt.Errorf("sample %q missing from optimized chunk", sample)
		}
	}
	return out, nil
}

func (c *optimizedArchiveCodec) decodeSample(chunkBytes []byte, dir optimizedSampleDirectory, streams optimizedStreamInfo) (map[string]any, error) {
	metaStart := streams.metaStart + dir.metaOffset
	metaEnd := metaStart + dir.metaLength
	if metaStart < 0 || metaEnd > len(chunkBytes) || metaStart > metaEnd {
		return nil, fmt.Errorf("metadata stream bounds are invalid")
	}
	metaReader := bytes.NewReader(chunkBytes[metaStart:metaEnd])
	metadataValue, err := c.decodeValue(metaReader)
	if err != nil {
		return nil, err
	}
	if metaReader.Len() != 0 {
		return nil, fmt.Errorf("metadata stream has trailing bytes")
	}
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("metadata value is not an object")
	}

	schemaStart := streams.schemaStart + dir.schemaOffset
	schemaEnd := schemaStart + dir.schemaLength
	if schemaStart < 0 || schemaEnd > len(chunkBytes) || schemaStart > schemaEnd {
		return nil, fmt.Errorf("feature schema stream bounds are invalid")
	}
	schemaReader := bytes.NewReader(chunkBytes[schemaStart:schemaEnd])
	featureSchemaIDs := make([]int, dir.featureCount)
	for i := 0; i < dir.featureCount; i++ {
		value, err := readUvarint(schemaReader)
		if err != nil {
			return nil, err
		}
		if value >= uint64(len(c.FeatureSchemas)) {
			return nil, fmt.Errorf("feature schema id %d is out of range", value)
		}
		featureSchemaIDs[i] = int(value)
	}
	if schemaReader.Len() != 0 {
		return nil, fmt.Errorf("feature schema stream has trailing bytes")
	}

	fieldCounts := make([]int, len(c.FeatureFields))
	for _, schemaID := range featureSchemaIDs {
		for _, key := range c.FeatureSchemas[schemaID].Keys {
			fieldID, ok := c.fieldIDs[key]
			if !ok {
				return nil, fmt.Errorf("feature schema refers to unknown field %q", key)
			}
			fieldCounts[fieldID]++
		}
	}

	fieldValues := make([][]any, len(c.FeatureFields))
	for fieldID, codec := range c.FieldCodecs {
		fieldStart := streams.fieldStarts[fieldID] + dir.fieldOffsets[fieldID]
		fieldEnd := fieldStart + dir.fieldLengths[fieldID]
		if fieldStart < 0 || fieldEnd > len(chunkBytes) || fieldStart > fieldEnd {
			return nil, fmt.Errorf("field %q stream bounds are invalid", codec.Field)
		}
		values, err := c.decodeFieldValues(codec, chunkBytes[fieldStart:fieldEnd], fieldCounts[fieldID], metadata)
		if err != nil {
			return nil, fmt.Errorf("decode field %q: %w", codec.Field, err)
		}
		fieldValues[fieldID] = values
	}

	fieldPositions := make([]int, len(c.FeatureFields))
	features := make([]any, 0, len(featureSchemaIDs))
	for _, schemaID := range featureSchemaIDs {
		feature := map[string]any{}
		for _, key := range c.FeatureSchemas[schemaID].Keys {
			fieldID := c.fieldIDs[key]
			pos := fieldPositions[fieldID]
			if pos >= len(fieldValues[fieldID]) {
				return nil, fmt.Errorf("field %q ran out of decoded values", key)
			}
			feature[key] = fieldValues[fieldID][pos]
			fieldPositions[fieldID]++
		}
		features = append(features, feature)
	}
	for fieldID, pos := range fieldPositions {
		if pos != len(fieldValues[fieldID]) {
			return nil, fmt.Errorf("field %q did not consume all decoded values", c.FeatureFields[fieldID])
		}
	}

	output := map[string]any{}
	for _, key := range c.TopKeys {
		if key == "features" {
			output[key] = features
			continue
		}
		value, ok := metadata[key]
		if !ok {
			return nil, fmt.Errorf("metadata missing top-level key %q", key)
		}
		output[key] = value
	}
	return output, nil
}

func parseOptimizedChunkDirectory(chunkBytes []byte, expectedFields int) ([]optimizedSampleDirectory, optimizedStreamInfo, error) {
	reader := bytes.NewReader(chunkBytes)
	magic := make([]byte, len(optimizedChunkMagic))
	if _, err := reader.Read(magic); err != nil {
		return nil, optimizedStreamInfo{}, err
	}
	if string(magic) != optimizedChunkMagic {
		return nil, optimizedStreamInfo{}, fmt.Errorf("not an optimized bakpack chunk")
	}
	nSamples, err := readUvarint(reader)
	if err != nil {
		return nil, optimizedStreamInfo{}, err
	}
	nFields, err := readUvarint(reader)
	if err != nil {
		return nil, optimizedStreamInfo{}, err
	}
	if int(nFields) != expectedFields {
		return nil, optimizedStreamInfo{}, fmt.Errorf("chunk field count %d does not match index field count %d", nFields, expectedFields)
	}
	metaLength, err := readUvarint(reader)
	if err != nil {
		return nil, optimizedStreamInfo{}, err
	}
	schemaLength, err := readUvarint(reader)
	if err != nil {
		return nil, optimizedStreamInfo{}, err
	}
	fieldLengths := make([]int, nFields)
	for i := 0; i < int(nFields); i++ {
		length, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		if length > uint64(len(chunkBytes)) {
			return nil, optimizedStreamInfo{}, fmt.Errorf("field stream length is out of range")
		}
		fieldLengths[i] = int(length)
	}

	dirs := make([]optimizedSampleDirectory, 0, nSamples)
	for i := 0; i < int(nSamples); i++ {
		sampleID, err := readString(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		filename, err := readString(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		featureCount, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		metaOffset, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		metaItemLength, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		schemaOffset, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		schemaItemLength, err := readUvarint(reader)
		if err != nil {
			return nil, optimizedStreamInfo{}, err
		}
		fieldOffsets := make([]int, nFields)
		sampleFieldLengths := make([]int, nFields)
		for fieldID := 0; fieldID < int(nFields); fieldID++ {
			offset, err := readUvarint(reader)
			if err != nil {
				return nil, optimizedStreamInfo{}, err
			}
			length, err := readUvarint(reader)
			if err != nil {
				return nil, optimizedStreamInfo{}, err
			}
			if offset > uint64(fieldLengths[fieldID]) || length > uint64(fieldLengths[fieldID]) || offset+length > uint64(fieldLengths[fieldID]) {
				return nil, optimizedStreamInfo{}, fmt.Errorf("sample field stream bounds are out of range")
			}
			fieldOffsets[fieldID] = int(offset)
			sampleFieldLengths[fieldID] = int(length)
		}
		if metaOffset > metaLength || metaItemLength > metaLength || metaOffset+metaItemLength > metaLength {
			return nil, optimizedStreamInfo{}, fmt.Errorf("sample metadata stream bounds are out of range")
		}
		if schemaOffset > schemaLength || schemaItemLength > schemaLength || schemaOffset+schemaItemLength > schemaLength {
			return nil, optimizedStreamInfo{}, fmt.Errorf("sample feature schema stream bounds are out of range")
		}
		dirs = append(dirs, optimizedSampleDirectory{
			sampleID:     sampleID,
			filename:     filename,
			featureCount: int(featureCount),
			metaOffset:   int(metaOffset),
			metaLength:   int(metaItemLength),
			schemaOffset: int(schemaOffset),
			schemaLength: int(schemaItemLength),
			fieldOffsets: fieldOffsets,
			fieldLengths: sampleFieldLengths,
		})
	}

	streamStart := len(chunkBytes) - reader.Len()
	metaStart := streamStart
	schemaStart := metaStart + int(metaLength)
	fieldStarts := make([]int, nFields)
	offset := schemaStart + int(schemaLength)
	for i, length := range fieldLengths {
		fieldStarts[i] = offset
		offset += length
	}
	if offset != len(chunkBytes) {
		return nil, optimizedStreamInfo{}, fmt.Errorf("chunk length does not match stream directory")
	}
	return dirs, optimizedStreamInfo{
		metaStart:   metaStart,
		schemaStart: schemaStart,
		fieldStarts: fieldStarts,
	}, nil
}

func (c *optimizedArchiveCodec) encodeFieldValues(codec FieldCodec, values []any, metadata map[string]any) ([]byte, error) {
	var out bytes.Buffer
	switch codec.Kind {
	case "sequence_index":
		idToIndex, err := sequenceIDIndex(metadata)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			contig, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected string contig")
			}
			index, ok := idToIndex[contig]
			if !ok {
				return nil, fmt.Errorf("contig %q not found in metadata sequences", contig)
			}
			writeUvarint(&out, uint64(index))
		}
	case "sample_prefix_uint_string":
		prefix, fixedWidth, numbers, err := parseSamplePrefixValues(values)
		if err != nil {
			return nil, err
		}
		writeString(&out, prefix)
		writeUvarint(&out, uint64(fixedWidth))
		for _, number := range numbers {
			writeUvarint(&out, number)
		}
	case "const_null":
		for _, value := range values {
			if value != nil {
				return nil, fmt.Errorf("expected null")
			}
		}
	case "const_bool":
		expected, ok := codec.Value.(bool)
		if !ok {
			return nil, fmt.Errorf("constant bool codec has non-bool value")
		}
		for _, value := range values {
			got, ok := value.(bool)
			if !ok || got != expected {
				return nil, fmt.Errorf("expected bool constant %v", expected)
			}
		}
	case "const_string":
		expected, ok := codec.Value.(string)
		if !ok {
			return nil, fmt.Errorf("constant string codec has non-string value")
		}
		for _, value := range values {
			got, ok := value.(string)
			if !ok || got != expected {
				return nil, fmt.Errorf("expected string constant %q", expected)
			}
		}
	case "bool_bitset":
		var current byte
		bit := 0
		for _, value := range values {
			got, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("expected bool")
			}
			if got {
				current |= 1 << bit
			}
			bit++
			if bit == 8 {
				out.WriteByte(current)
				current = 0
				bit = 0
			}
		}
		if bit != 0 {
			out.WriteByte(current)
		}
	case "uint":
		for _, value := range values {
			number, ok := jsonUint64(value)
			if !ok {
				return nil, fmt.Errorf("expected non-negative integer")
			}
			writeUvarint(&out, number)
		}
	case "int":
		for _, value := range values {
			number, ok := jsonInt64(value)
			if !ok {
				return nil, fmt.Errorf("expected integer")
			}
			writeUvarint(&out, zigzagInt64(number))
		}
	case "float64":
		for _, value := range values {
			number, ok := jsonFloat64(value)
			if !ok {
				return nil, fmt.Errorf("expected float")
			}
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(number))
			out.Write(buf[:])
		}
	case "raw_number":
		for _, value := range values {
			text, ok := jsonNumberText(value)
			if !ok {
				return nil, fmt.Errorf("expected JSON number")
			}
			writeString(&out, text)
		}
	case "raw_string":
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected string")
			}
			writeString(&out, text)
		}
	case "nullable_raw_string":
		for _, value := range values {
			if value == nil {
				writeUvarint(&out, 0)
				continue
			}
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected nullable string")
			}
			encoded := []byte(text)
			writeUvarint(&out, uint64(len(encoded)+1))
			out.Write(encoded)
		}
	case "enum_string":
		idByValue := map[string]int{}
		for i, value := range codec.Values {
			idByValue[value] = i
		}
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected enum string")
			}
			id, ok := idByValue[text]
			if !ok {
				return nil, fmt.Errorf("enum value %q not found in dictionary", text)
			}
			writeUvarint(&out, uint64(id))
		}
	case "nullable_enum_string":
		idByValue := map[string]int{}
		for i, value := range codec.Values {
			idByValue[value] = i + 1
		}
		for _, value := range values {
			if value == nil {
				writeUvarint(&out, 0)
				continue
			}
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("expected nullable enum string")
			}
			id, ok := idByValue[text]
			if !ok {
				return nil, fmt.Errorf("enum value %q not found in dictionary", text)
			}
			writeUvarint(&out, uint64(id))
		}
	case "generic":
		for _, value := range values {
			if err := c.encodeValue(&out, value); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unknown field codec %q", codec.Kind)
	}
	return out.Bytes(), nil
}

func (c *optimizedArchiveCodec) decodeFieldValues(codec FieldCodec, data []byte, count int, metadata map[string]any) ([]any, error) {
	if count == 0 && codec.Kind != "sample_prefix_uint_string" {
		if len(data) != 0 {
			return nil, fmt.Errorf("empty field has payload bytes")
		}
	}
	switch codec.Kind {
	case "sequence_index":
		sequenceIDs, err := sequenceIDs(metadata)
		if err != nil {
			return nil, err
		}
		reader := bytes.NewReader(data)
		values := make([]any, count)
		for i := 0; i < count; i++ {
			index, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			if index >= uint64(len(sequenceIDs)) {
				return nil, fmt.Errorf("sequence index %d is out of range", index)
			}
			values[i] = sequenceIDs[index]
		}
		if reader.Len() != 0 {
			return nil, fmt.Errorf("field stream has trailing bytes")
		}
		return values, nil
	case "sample_prefix_uint_string":
		reader := bytes.NewReader(data)
		prefix, err := readString(reader)
		if err != nil {
			return nil, err
		}
		fixedWidth, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		values := make([]any, count)
		for i := 0; i < count; i++ {
			number, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			suffix := strconv.FormatUint(number, 10)
			if fixedWidth > 0 && len(suffix) < int(fixedWidth) {
				suffix = strings.Repeat("0", int(fixedWidth)-len(suffix)) + suffix
			}
			values[i] = prefix + suffix
		}
		if reader.Len() != 0 {
			return nil, fmt.Errorf("field stream has trailing bytes")
		}
		return values, nil
	case "const_null":
		if len(data) != 0 {
			return nil, fmt.Errorf("constant field has payload bytes")
		}
		values := make([]any, count)
		return values, nil
	case "const_bool":
		if len(data) != 0 {
			return nil, fmt.Errorf("constant field has payload bytes")
		}
		values := make([]any, count)
		for i := range values {
			values[i] = codec.Value.(bool)
		}
		return values, nil
	case "const_string":
		if len(data) != 0 {
			return nil, fmt.Errorf("constant field has payload bytes")
		}
		values := make([]any, count)
		for i := range values {
			values[i] = codec.Value.(string)
		}
		return values, nil
	case "bool_bitset":
		if len(data) != (count+7)/8 {
			return nil, fmt.Errorf("bool bitset length mismatch")
		}
		values := make([]any, count)
		for i := 0; i < count; i++ {
			values[i] = data[i/8]&(1<<(i%8)) != 0
		}
		return values, nil
	}

	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	switch codec.Kind {
	case "uint":
		for i := 0; i < count; i++ {
			value, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, json.Number(strconv.FormatUint(value, 10)))
		}
	case "int":
		for i := 0; i < count; i++ {
			value, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, json.Number(strconv.FormatInt(unzigzagInt64(value), 10)))
		}
	case "float64":
		for i := 0; i < count; i++ {
			var raw [8]byte
			if _, err := reader.Read(raw[:]); err != nil {
				return nil, err
			}
			values = append(values, math.Float64frombits(binary.LittleEndian.Uint64(raw[:])))
		}
	case "raw_number":
		for i := 0; i < count; i++ {
			value, err := readString(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, json.Number(value))
		}
	case "raw_string":
		for i := 0; i < count; i++ {
			value, err := readString(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	case "nullable_raw_string":
		for i := 0; i < count; i++ {
			encodedLength, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			if encodedLength == 0 {
				values = append(values, nil)
				continue
			}
			if encodedLength-1 > uint64(reader.Len()) {
				return nil, fmt.Errorf("nullable string length is out of range")
			}
			raw := make([]byte, encodedLength-1)
			if _, err := reader.Read(raw); err != nil {
				return nil, err
			}
			values = append(values, string(raw))
		}
	case "enum_string":
		for i := 0; i < count; i++ {
			id, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			if id >= uint64(len(codec.Values)) {
				return nil, fmt.Errorf("enum id %d is out of range", id)
			}
			values = append(values, codec.Values[id])
		}
	case "nullable_enum_string":
		for i := 0; i < count; i++ {
			id, err := readUvarint(reader)
			if err != nil {
				return nil, err
			}
			if id == 0 {
				values = append(values, nil)
				continue
			}
			if id-1 >= uint64(len(codec.Values)) {
				return nil, fmt.Errorf("enum id %d is out of range", id)
			}
			values = append(values, codec.Values[id-1])
		}
	case "generic":
		for i := 0; i < count; i++ {
			value, err := c.decodeValue(reader)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
	default:
		return nil, fmt.Errorf("unknown field codec %q", codec.Kind)
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("field stream has trailing bytes")
	}
	return values, nil
}

func (c *optimizedArchiveCodec) encodeValue(out *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		out.WriteByte(valueTagNull)
	case bool:
		if v {
			out.WriteByte(valueTagTrue)
		} else {
			out.WriteByte(valueTagFalse)
		}
	case json.Number:
		if number, ok := jsonNumberInt64(v); ok {
			out.WriteByte(valueTagInt)
			writeUvarint(out, zigzagInt64(number))
			return nil
		}
		out.WriteByte(valueTagNumber)
		writeString(out, v.String())
	case int:
		out.WriteByte(valueTagInt)
		writeUvarint(out, zigzagInt64(int64(v)))
	case int64:
		out.WriteByte(valueTagInt)
		writeUvarint(out, zigzagInt64(v))
	case float64:
		out.WriteByte(valueTagFloat)
		var raw [8]byte
		binary.LittleEndian.PutUint64(raw[:], math.Float64bits(v))
		out.Write(raw[:])
	case string:
		out.WriteByte(valueTagString)
		writeString(out, v)
	case []any:
		out.WriteByte(valueTagList)
		writeUvarint(out, uint64(len(v)))
		for _, item := range v {
			if err := c.encodeValue(out, item); err != nil {
				return err
			}
		}
	case map[string]any:
		keys := sortedObjectKeys(v)
		schemaID, ok := c.valueSchemaIDs[schemaKey(keys)]
		if !ok {
			return fmt.Errorf("unknown value schema %v", keys)
		}
		out.WriteByte(valueTagObject)
		writeUvarint(out, uint64(schemaID))
		for _, key := range keys {
			if err := c.encodeValue(out, v[key]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported JSON value type %T", value)
	}
	return nil
}

func (c *optimizedArchiveCodec) decodeValue(reader *bytes.Reader) (any, error) {
	tag, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	switch tag {
	case valueTagNull:
		return nil, nil
	case valueTagFalse:
		return false, nil
	case valueTagTrue:
		return true, nil
	case valueTagInt:
		value, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		return json.Number(strconv.FormatInt(unzigzagInt64(value), 10)), nil
	case valueTagFloat:
		var raw [8]byte
		if _, err := reader.Read(raw[:]); err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(raw[:])), nil
	case valueTagString:
		return readString(reader)
	case valueTagList:
		count, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		values := make([]any, count)
		for i := range values {
			value, err := c.decodeValue(reader)
			if err != nil {
				return nil, err
			}
			values[i] = value
		}
		return values, nil
	case valueTagObject:
		schemaID, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		if schemaID >= uint64(len(c.ValueSchemas)) {
			return nil, fmt.Errorf("value schema id %d is out of range", schemaID)
		}
		keys := c.ValueSchemas[schemaID].Keys
		value := map[string]any{}
		for _, key := range keys {
			item, err := c.decodeValue(reader)
			if err != nil {
				return nil, err
			}
			value[key] = item
		}
		return value, nil
	case valueTagNumber:
		value, err := readString(reader)
		if err != nil {
			return nil, err
		}
		return json.Number(value), nil
	default:
		return nil, fmt.Errorf("unknown value tag %d", tag)
	}
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
		return FieldCodec{Field: field, Kind: "sequence_index"}
	}
	if (field == "id" || field == "locus") && stats.prefixCandidate {
		return FieldCodec{Field: field, Kind: "sample_prefix_uint_string"}
	}
	if onlyType(stats.types, "null") {
		return FieldCodec{Field: field, Kind: "const_null"}
	}
	if onlyType(stats.types, "bool") {
		if len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: "const_bool", Value: stats.scalarValues[0]}
		}
		return FieldCodec{Field: field, Kind: "bool_bitset"}
	}
	if onlyType(stats.types, "int") {
		if stats.minInt != nil && *stats.minInt >= 0 {
			return FieldCodec{Field: field, Kind: "uint"}
		}
		return FieldCodec{Field: field, Kind: "int"}
	}
	if onlyType(stats.types, "float") {
		return FieldCodec{Field: field, Kind: "raw_number"}
	}
	if onlyType(stats.types, "string") {
		if len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: "const_string", Value: stats.scalarValues[0]}
		}
		if len(stats.scalarValues) <= 256 {
			return FieldCodec{Field: field, Kind: "enum_string", Values: scalarStrings(stats.scalarValues)}
		}
		return FieldCodec{Field: field, Kind: "raw_string"}
	}
	if typeSubset(stats.types, "string", "null") {
		stringsOnly := scalarStringsWithoutNull(stats.scalarValues)
		if len(stringsOnly) == 0 {
			return FieldCodec{Field: field, Kind: "const_null"}
		}
		if len(stringsOnly) == 1 && len(stats.scalarValues) == 1 {
			return FieldCodec{Field: field, Kind: "const_string", Value: stringsOnly[0]}
		}
		if len(stringsOnly) <= 256 {
			return FieldCodec{Field: field, Kind: "nullable_enum_string", Values: stringsOnly}
		}
		return FieldCodec{Field: field, Kind: "nullable_raw_string"}
	}
	return FieldCodec{Field: field, Kind: "generic"}
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

func sequenceIDIndex(metadata map[string]any) (map[string]int, error) {
	ids, err := sequenceIDs(metadata)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for i, id := range ids {
		out[id] = i
	}
	return out, nil
}

func sequenceIDs(metadata map[string]any) ([]string, error) {
	raw, ok := metadata["sequences"].([]any)
	if !ok {
		return nil, fmt.Errorf("sequence_index codec requires metadata sequences list")
	}
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("sequence entry is not an object")
		}
		id, ok := entry["id"].(string)
		if !ok {
			return nil, fmt.Errorf("sequence entry has no string id")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseSamplePrefixValues(values []any) (string, int, []uint64, error) {
	var prefix string
	var suffixWidth int
	var fixedWidth int
	numbers := make([]uint64, 0, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return "", 0, nil, fmt.Errorf("expected string")
		}
		itemPrefix, suffix, number, ok := splitTrailingNumber(text)
		if !ok {
			return "", 0, nil, fmt.Errorf("value %q has no numeric suffix", text)
		}
		if i == 0 {
			prefix = itemPrefix
			suffixWidth = len(suffix)
		} else if itemPrefix != prefix {
			return "", 0, nil, fmt.Errorf("values have more than one prefix")
		}
		if len(suffix) > 1 && suffix[0] == '0' {
			fixedWidth = suffixWidth
		}
		if fixedWidth > 0 && len(suffix) != suffixWidth {
			return "", 0, nil, fmt.Errorf("zero-padded suffixes have mixed widths")
		}
		numbers = append(numbers, number)
	}
	return prefix, fixedWidth, numbers, nil
}

func splitTrailingNumber(value string) (string, string, uint64, bool) {
	if value == "" {
		return "", "", 0, false
	}
	i := len(value)
	for i > 0 && value[i-1] >= '0' && value[i-1] <= '9' {
		i--
	}
	if i == len(value) {
		return "", "", 0, false
	}
	number, err := strconv.ParseUint(value[i:], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return value[:i], value[i:], number, true
}

func jsonInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case json.Number:
		return jsonNumberInt64(v)
	case int:
		return int64(v), true
	case int64:
		return v, true
	default:
		return 0, false
	}
}

func jsonUint64(value any) (uint64, bool) {
	number, ok := jsonInt64(value)
	if !ok || number < 0 {
		return 0, false
	}
	return uint64(number), true
}

func jsonFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		if _, ok := jsonNumberInt64(v); ok {
			return 0, false
		}
		number, err := v.Float64()
		return number, err == nil
	case float64:
		return v, true
	default:
		return 0, false
	}
}

func jsonNumberText(value any) (string, bool) {
	switch v := value.(type) {
	case json.Number:
		return v.String(), true
	case int:
		return strconv.FormatInt(int64(v), 10), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), true
	default:
		return "", false
	}
}

func jsonNumberInt64(value json.Number) (int64, bool) {
	number, err := value.Int64()
	if err == nil {
		return number, true
	}
	return 0, false
}

func zigzagInt64(value int64) uint64 {
	return uint64(value<<1) ^ uint64(value>>63)
}

func unzigzagInt64(value uint64) int64 {
	return int64(value>>1) ^ -int64(value&1)
}
