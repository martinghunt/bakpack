package bakpack

import "fmt"

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

const (
	fieldCodecSequenceIndex          = "sequence_index"
	fieldCodecSamplePrefixUintString = "sample_prefix_uint_string"
	fieldCodecConstNull              = "const_null"
	fieldCodecConstBool              = "const_bool"
	fieldCodecConstString            = "const_string"
	fieldCodecBoolBitset             = "bool_bitset"
	fieldCodecUint                   = "uint"
	fieldCodecInt                    = "int"
	fieldCodecFloat64                = "float64"
	fieldCodecRawNumber              = "raw_number"
	fieldCodecRawString              = "raw_string"
	fieldCodecNullableRawString      = "nullable_raw_string"
	fieldCodecEnumString             = "enum_string"
	fieldCodecNullableEnumString     = "nullable_enum_string"
	fieldCodecGeneric                = "generic"
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
	for _, fieldCodec := range codec.FieldCodecs {
		if err := validateFeatureTypeFieldCodec(fieldCodec); err != nil {
			return nil, err
		}
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
