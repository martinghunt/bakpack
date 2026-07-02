package bakpack

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (c *optimizedArchiveCodec) encodeFieldValues(codec FieldCodec, values []any, metadata map[string]any) ([]byte, error) {
	switch codec.Kind {
	case fieldCodecSequenceIndex:
		return encodeSequenceIndexFieldValues(values, metadata)
	case fieldCodecSamplePrefixUintString:
		return encodeSamplePrefixUintStringFieldValues(values)
	case fieldCodecConstNull:
		return encodeConstNullFieldValues(values)
	case fieldCodecConstBool:
		return encodeConstBoolFieldValues(codec, values)
	case fieldCodecConstString:
		return encodeConstStringFieldValues(codec, values)
	case fieldCodecBoolBitset:
		return encodeBoolBitsetFieldValues(values)
	case fieldCodecUint:
		return encodeUintFieldValues(values)
	case fieldCodecInt:
		return encodeIntFieldValues(values)
	case fieldCodecFloat64:
		return encodeFloat64FieldValues(values)
	case fieldCodecRawNumber:
		return encodeRawNumberFieldValues(values)
	case fieldCodecRawString:
		return encodeRawStringFieldValues(values)
	case fieldCodecNullableRawString:
		return encodeNullableRawStringFieldValues(values)
	case fieldCodecEnumString:
		return encodeEnumStringFieldValues(codec, values, false)
	case fieldCodecNullableEnumString:
		return encodeEnumStringFieldValues(codec, values, true)
	case fieldCodecGeneric:
		return c.encodeGenericFieldValues(values)
	default:
		return nil, fmt.Errorf("unknown field codec %q", codec.Kind)
	}
}

func encodeSequenceIndexFieldValues(values []any, metadata map[string]any) ([]byte, error) {
	var out bytes.Buffer
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
	return out.Bytes(), nil
}

func encodeSamplePrefixUintStringFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	prefix, fixedWidth, numbers, err := parseSamplePrefixValues(values)
	if err != nil {
		return nil, err
	}
	writeString(&out, prefix)
	writeUvarint(&out, uint64(fixedWidth))
	for _, number := range numbers {
		writeUvarint(&out, number)
	}
	return out.Bytes(), nil
}

func encodeConstNullFieldValues(values []any) ([]byte, error) {
	for _, value := range values {
		if value != nil {
			return nil, fmt.Errorf("expected null")
		}
	}
	return nil, nil
}

func encodeConstBoolFieldValues(codec FieldCodec, values []any) ([]byte, error) {
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
	return nil, nil
}

func encodeConstStringFieldValues(codec FieldCodec, values []any) ([]byte, error) {
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
	return nil, nil
}

func encodeBoolBitsetFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
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
	return out.Bytes(), nil
}

func encodeUintFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		number, ok := jsonUint64(value)
		if !ok {
			return nil, fmt.Errorf("expected non-negative integer")
		}
		writeUvarint(&out, number)
	}
	return out.Bytes(), nil
}

func encodeIntFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		number, ok := jsonInt64(value)
		if !ok {
			return nil, fmt.Errorf("expected integer")
		}
		writeUvarint(&out, zigzagInt64(number))
	}
	return out.Bytes(), nil
}

func encodeFloat64FieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		number, ok := jsonFloat64(value)
		if !ok {
			return nil, fmt.Errorf("expected float")
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(number))
		out.Write(buf[:])
	}
	return out.Bytes(), nil
}

func encodeRawNumberFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		text, ok := jsonNumberText(value)
		if !ok {
			return nil, fmt.Errorf("expected JSON number")
		}
		writeString(&out, text)
	}
	return out.Bytes(), nil
}

func encodeRawStringFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("expected string")
		}
		writeString(&out, text)
	}
	return out.Bytes(), nil
}

func encodeNullableRawStringFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
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
	return out.Bytes(), nil
}

func encodeEnumStringFieldValues(codec FieldCodec, values []any, nullable bool) ([]byte, error) {
	var out bytes.Buffer
	idByValue := map[string]int{}
	for i, value := range codec.Values {
		if nullable {
			idByValue[value] = i + 1
		} else {
			idByValue[value] = i
		}
	}
	for _, value := range values {
		if nullable && value == nil {
			writeUvarint(&out, 0)
			continue
		}
		text, ok := value.(string)
		if !ok {
			if nullable {
				return nil, fmt.Errorf("expected nullable enum string")
			}
			return nil, fmt.Errorf("expected enum string")
		}
		id, ok := idByValue[text]
		if !ok {
			return nil, fmt.Errorf("enum value %q not found in dictionary", text)
		}
		writeUvarint(&out, uint64(id))
	}
	return out.Bytes(), nil
}

func (c *optimizedArchiveCodec) encodeGenericFieldValues(values []any) ([]byte, error) {
	var out bytes.Buffer
	for _, value := range values {
		if err := c.encodeValue(&out, value); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func (c *optimizedArchiveCodec) decodeFieldValues(codec FieldCodec, data []byte, count int, metadata map[string]any) ([]any, error) {
	if count == 0 && codec.Kind != fieldCodecSamplePrefixUintString {
		if len(data) != 0 {
			return nil, fmt.Errorf("empty field has payload bytes")
		}
	}
	switch codec.Kind {
	case fieldCodecSequenceIndex:
		return decodeSequenceIndexFieldValues(data, count, metadata)
	case fieldCodecSamplePrefixUintString:
		return decodeSamplePrefixUintStringFieldValues(data, count)
	case fieldCodecConstNull:
		return decodeConstNullFieldValues(data, count)
	case fieldCodecConstBool:
		return decodeConstBoolFieldValues(codec, data, count)
	case fieldCodecConstString:
		return decodeConstStringFieldValues(codec, data, count)
	case fieldCodecBoolBitset:
		return decodeBoolBitsetFieldValues(data, count)
	case fieldCodecUint:
		return decodeUintFieldValues(data, count)
	case fieldCodecInt:
		return decodeIntFieldValues(data, count)
	case fieldCodecFloat64:
		return decodeFloat64FieldValues(data, count)
	case fieldCodecRawNumber:
		return decodeRawNumberFieldValues(data, count)
	case fieldCodecRawString:
		return decodeRawStringFieldValues(data, count)
	case fieldCodecNullableRawString:
		return decodeNullableRawStringFieldValues(data, count)
	case fieldCodecEnumString:
		return decodeEnumStringFieldValues(codec, data, count, false)
	case fieldCodecNullableEnumString:
		return decodeEnumStringFieldValues(codec, data, count, true)
	case fieldCodecGeneric:
		return c.decodeGenericFieldValues(data, count)
	default:
		return nil, fmt.Errorf("unknown field codec %q", codec.Kind)
	}
}

func decodeSequenceIndexFieldValues(data []byte, count int, metadata map[string]any) ([]any, error) {
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
}

func decodeSamplePrefixUintStringFieldValues(data []byte, count int) ([]any, error) {
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
}

func decodeConstNullFieldValues(data []byte, count int) ([]any, error) {
	if len(data) != 0 {
		return nil, fmt.Errorf("constant field has payload bytes")
	}
	return make([]any, count), nil
}

func decodeConstBoolFieldValues(codec FieldCodec, data []byte, count int) ([]any, error) {
	if len(data) != 0 {
		return nil, fmt.Errorf("constant field has payload bytes")
	}
	values := make([]any, count)
	for i := range values {
		values[i] = codec.Value.(bool)
	}
	return values, nil
}

func decodeConstStringFieldValues(codec FieldCodec, data []byte, count int) ([]any, error) {
	if len(data) != 0 {
		return nil, fmt.Errorf("constant field has payload bytes")
	}
	values := make([]any, count)
	for i := range values {
		values[i] = codec.Value.(string)
	}
	return values, nil
}

func decodeBoolBitsetFieldValues(data []byte, count int) ([]any, error) {
	if len(data) != (count+7)/8 {
		return nil, fmt.Errorf("bool bitset length mismatch")
	}
	values := make([]any, count)
	for i := 0; i < count; i++ {
		values[i] = data[i/8]&(1<<(i%8)) != 0
	}
	return values, nil
}

func decodeUintFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		value, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, json.Number(strconv.FormatUint(value, 10)))
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeIntFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		value, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, json.Number(strconv.FormatInt(unzigzagInt64(value), 10)))
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeFloat64FieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		var raw [8]byte
		if _, err := reader.Read(raw[:]); err != nil {
			return nil, err
		}
		values = append(values, math.Float64frombits(binary.LittleEndian.Uint64(raw[:])))
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeRawNumberFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		value, err := readString(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, json.Number(value))
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeRawStringFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		value, err := readString(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeNullableRawStringFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
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
		if encodedLength == 1 {
			values = append(values, "")
			continue
		}
		raw := make([]byte, encodedLength-1)
		if _, err := reader.Read(raw); err != nil {
			return nil, err
		}
		values = append(values, string(raw))
	}
	return finishDecodedFieldValues(reader, values)
}

func decodeEnumStringFieldValues(codec FieldCodec, data []byte, count int, nullable bool) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		id, err := readUvarint(reader)
		if err != nil {
			return nil, err
		}
		if nullable && id == 0 {
			values = append(values, nil)
			continue
		}
		if nullable {
			if id-1 >= uint64(len(codec.Values)) {
				return nil, fmt.Errorf("enum id %d is out of range", id)
			}
			values = append(values, codec.Values[id-1])
			continue
		}
		if id >= uint64(len(codec.Values)) {
			return nil, fmt.Errorf("enum id %d is out of range", id)
		}
		values = append(values, codec.Values[id])
	}
	return finishDecodedFieldValues(reader, values)
}

func (c *optimizedArchiveCodec) decodeGenericFieldValues(data []byte, count int) ([]any, error) {
	reader := bytes.NewReader(data)
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		value, err := c.decodeValue(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return finishDecodedFieldValues(reader, values)
}

func finishDecodedFieldValues(reader *bytes.Reader, values []any) ([]any, error) {
	if reader.Len() != 0 {
		return nil, fmt.Errorf("field stream has trailing bytes")
	}
	return values, nil
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
