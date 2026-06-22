package bakpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
)

func DecodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func PrettyJSON(value any) ([]byte, error) {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func CanonicalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func CanonicalJSONSHA256(value any) (string, error) {
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return SHA256Hex(data), nil
}

func JSONBytesCanonicalSHA256(data []byte) (string, error) {
	value, err := DecodeJSON(data)
	if err != nil {
		return "", err
	}
	return CanonicalJSONSHA256(value)
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeCanonicalJSON(w io.Writer, value any) error {
	switch v := value.(type) {
	case nil:
		_, err := io.WriteString(w, "null")
		return err
	case bool:
		if v {
			_, err := io.WriteString(w, "true")
			return err
		}
		_, err := io.WriteString(w, "false")
		return err
	case string:
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(encoded)
		return err
	case json.Number:
		if _, err := v.Int64(); err == nil {
			_, err = io.WriteString(w, v.String())
			return err
		}
		f, err := v.Float64()
		if err != nil {
			return fmt.Errorf("invalid JSON number %q", v.String())
		}
		_, err = io.WriteString(w, strconv.FormatFloat(f, 'g', -1, 64))
		return err
	case float64:
		_, err := io.WriteString(w, strconv.FormatFloat(v, 'g', -1, 64))
		return err
	case []any:
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		for i, item := range v {
			if i > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			if err := writeCanonicalJSON(w, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if _, err := io.WriteString(w, "{"); err != nil {
			return err
		}
		for i, key := range keys {
			if i > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			encodedKey, err := json.Marshal(key)
			if err != nil {
				return err
			}
			if _, err := w.Write(encodedKey); err != nil {
				return err
			}
			if _, err := io.WriteString(w, ":"); err != nil {
				return err
			}
			if err := writeCanonicalJSON(w, v[key]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "}")
		return err
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("unsupported JSON value %T: %w", value, err)
		}
		_, err = w.Write(encoded)
		return err
	}
}
