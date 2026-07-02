package bakpack

import (
	"bytes"
	"fmt"
)

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
	if err := validateBaktaFeatureTypes(output); err != nil {
		return nil, err
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
