package bakpack

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

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
