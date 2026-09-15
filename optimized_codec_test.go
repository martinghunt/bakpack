package bakpack

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestChooseFieldCodecFocusedCases(t *testing.T) {
	nullableRawValues := []any{nil}
	for i := 0; i < 257; i++ {
		nullableRawValues = append(nullableRawValues, "value_"+strconv.Itoa(i))
	}

	tests := []struct {
		name       string
		field      string
		values     []any
		wantKind   string
		wantValue  any
		wantValues []string
	}{
		{
			name:     "sequence index for contig",
			field:    "contig",
			values:   []any{"contig1", "contig2"},
			wantKind: "sequence_index",
		},
		{
			name:     "sample prefix ids",
			field:    "id",
			values:   []any{"gene_00001", "gene_00002"},
			wantKind: "sample_prefix_uint_string",
		},
		{
			name:     "bool bitset",
			field:    "pseudo",
			values:   []any{true, false, true},
			wantKind: "bool_bitset",
		},
		{
			name:     "unsigned ints",
			field:    "start",
			values:   []any{json.Number("1"), json.Number("300")},
			wantKind: "uint",
		},
		{
			name:     "signed ints",
			field:    "score",
			values:   []any{json.Number("-5"), json.Number("7")},
			wantKind: "int",
		},
		{
			name:     "raw float numbers",
			field:    "evalue",
			values:   []any{json.Number("1.5"), json.Number("2e-4")},
			wantKind: "raw_number",
		},
		{
			name:       "enum strings",
			field:      "product",
			values:     []any{"alpha", "beta", "alpha"},
			wantKind:   "enum_string",
			wantValues: []string{"alpha", "beta"},
		},
		{
			name:       "nullable enum strings",
			field:      "gene",
			values:     []any{"abc", nil, "def"},
			wantKind:   "nullable_enum_string",
			wantValues: []string{"abc", "def"},
		},
		{
			name:     "nullable raw strings",
			field:    "note",
			values:   nullableRawValues,
			wantKind: "nullable_raw_string",
		},
		{
			name:      "constant string",
			field:     "type",
			values:    []any{"cds", "cds"},
			wantKind:  "const_string",
			wantValue: "cds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseFieldCodec(tt.field, fieldStatsForTest(tt.field, tt.values))
			if got.Field != tt.field {
				t.Fatalf("field = %q, want %q", got.Field, tt.field)
			}
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q; codec=%#v", got.Kind, tt.wantKind, got)
			}
			if tt.wantValue != nil && !reflect.DeepEqual(got.Value, tt.wantValue) {
				t.Fatalf("value = %#v, want %#v", got.Value, tt.wantValue)
			}
			if tt.wantValues != nil && !reflect.DeepEqual(got.Values, tt.wantValues) {
				t.Fatalf("values = %#v, want %#v", got.Values, tt.wantValues)
			}
		})
	}
}

func TestOptimizedFieldCodecRoundTripsFocusedCases(t *testing.T) {
	sequenceMetadata := map[string]any{
		"sequences": []any{
			map[string]any{"id": "contig1"},
			map[string]any{"id": "contig2"},
			map[string]any{"id": "contig3"},
		},
	}

	tests := []struct {
		name     string
		codec    FieldCodec
		values   []any
		want     []any
		metadata map[string]any
	}{
		{
			name:     "sequence index",
			codec:    FieldCodec{Field: "contig", Kind: "sequence_index"},
			values:   []any{"contig2", "contig1", "contig3"},
			want:     []any{"contig2", "contig1", "contig3"},
			metadata: sequenceMetadata,
		},
		{
			name:   "sample prefix ids",
			codec:  FieldCodec{Field: "id", Kind: "sample_prefix_uint_string"},
			values: []any{"gene_00001", "gene_00012"},
			want:   []any{"gene_00001", "gene_00012"},
		},
		{
			name:   "bool bitset",
			codec:  FieldCodec{Field: "pseudo", Kind: "bool_bitset"},
			values: []any{true, false, true, false, true, false, true, false, true},
			want:   []any{true, false, true, false, true, false, true, false, true},
		},
		{
			name:   "unsigned ints",
			codec:  FieldCodec{Field: "start", Kind: "uint"},
			values: []any{json.Number("0"), json.Number("42"), json.Number("9001")},
			want:   []any{json.Number("0"), json.Number("42"), json.Number("9001")},
		},
		{
			name:   "signed ints",
			codec:  FieldCodec{Field: "score", Kind: "int"},
			values: []any{json.Number("-7"), json.Number("0"), json.Number("12")},
			want:   []any{json.Number("-7"), json.Number("0"), json.Number("12")},
		},
		{
			name:   "raw numbers",
			codec:  FieldCodec{Field: "evalue", Kind: "raw_number"},
			values: []any{json.Number("1.25"), json.Number("2e-6")},
			want:   []any{json.Number("1.25"), json.Number("2e-6")},
		},
		{
			name:   "float64",
			codec:  FieldCodec{Field: "ratio", Kind: "float64"},
			values: []any{1.25, -2.5},
			want:   []any{1.25, -2.5},
		},
		{
			name:   "enum strings",
			codec:  FieldCodec{Field: "product", Kind: "enum_string", Values: []string{"alpha", "beta"}},
			values: []any{"beta", "alpha", "beta"},
			want:   []any{"beta", "alpha", "beta"},
		},
		{
			name:   "nullable enum strings",
			codec:  FieldCodec{Field: "gene", Kind: "nullable_enum_string", Values: []string{"abc", "def"}},
			values: []any{"abc", nil, "def"},
			want:   []any{"abc", nil, "def"},
		},
		{
			name:   "nullable raw strings",
			codec:  FieldCodec{Field: "note", Kind: "nullable_raw_string"},
			values: []any{"long free text", nil, ""},
			want:   []any{"long free text", nil, ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := roundTripFieldValuesForTest(t, tt.codec, tt.values, tt.metadata)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("round trip values = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func fieldStatsForTest(field string, values []any) *fieldStats {
	stats := newFieldStats()
	for _, value := range values {
		stats.add(value)
	}
	stats.addSampleValues(field, values)
	return stats
}

func TestParseOptimizedChunkDirectoryRejectsOversizedSampleCount(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(optimizedChunkMagic)
	writeUvarint(&buf, 1<<60) // nSamples: absurdly large relative to chunk size
	writeUvarint(&buf, 0)     // nFields
	writeUvarint(&buf, 0)     // metaLength
	writeUvarint(&buf, 0)     // schemaLength

	_, _, err := parseOptimizedChunkDirectory(buf.Bytes(), 0)
	if err == nil {
		t.Fatal("parseOptimizedChunkDirectory() with oversized sample count = nil error, want error")
	}
	if !strings.Contains(err.Error(), "sample count") {
		t.Fatalf("parseOptimizedChunkDirectory() error = %v, want mention of sample count", err)
	}
}

func TestParseOptimizedChunkDirectoryRejectsOversizedFeatureCount(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(optimizedChunkMagic)
	writeUvarint(&buf, 1) // nSamples
	writeUvarint(&buf, 0) // nFields
	writeUvarint(&buf, 0) // metaLength
	writeUvarint(&buf, 0) // schemaLength

	writeString(&buf, "sample1")      // sampleID
	writeString(&buf, "sample1.json") // filename
	writeUvarint(&buf, 1<<20)         // featureCount: far larger than the schema stream below
	writeUvarint(&buf, 0)             // metaOffset
	writeUvarint(&buf, 0)             // metaItemLength
	writeUvarint(&buf, 0)             // schemaOffset
	writeUvarint(&buf, 0)             // schemaItemLength

	_, _, err := parseOptimizedChunkDirectory(buf.Bytes(), 0)
	if err == nil {
		t.Fatal("parseOptimizedChunkDirectory() with oversized feature count = nil error, want error")
	}
	if !strings.Contains(err.Error(), "feature count") {
		t.Fatalf("parseOptimizedChunkDirectory() error = %v, want mention of feature count", err)
	}
}

func TestDecodeValueRejectsOversizedListCount(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(valueTagList)
	writeUvarint(&buf, 1<<60) // count: far larger than any remaining data

	c := &optimizedArchiveCodec{}
	_, err := c.decodeValue(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("decodeValue() with oversized list count = nil error, want error")
	}
	if !strings.Contains(err.Error(), "list value count") {
		t.Fatalf("decodeValue() error = %v, want mention of list value count", err)
	}
}

func TestDecodeConstFieldValuesRejectsMismatchedCodecValueType(t *testing.T) {
	if _, err := decodeConstBoolFieldValues(FieldCodec{Value: "not-a-bool"}, nil, 3); err == nil {
		t.Fatal("decodeConstBoolFieldValues() with non-bool codec value = nil error, want error")
	}
	if _, err := decodeConstStringFieldValues(FieldCodec{Value: 42}, nil, 3); err == nil {
		t.Fatal("decodeConstStringFieldValues() with non-string codec value = nil error, want error")
	}
}

func roundTripFieldValuesForTest(t *testing.T, codec FieldCodec, values []any, metadata map[string]any) []any {
	t.Helper()
	c := &optimizedArchiveCodec{}
	encoded, err := c.encodeFieldValues(codec, values, metadata)
	if err != nil {
		t.Fatalf("encodeFieldValues(%s) error = %v", codec.Kind, err)
	}
	decoded, err := c.decodeFieldValues(codec, encoded, len(values), metadata)
	if err != nil {
		t.Fatalf("decodeFieldValues(%s) error = %v", codec.Kind, err)
	}
	return decoded
}
