// Package canon implements the canonical JSON representation used by state
// and integrity boundaries.
package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const maxExpandedNumberBytes = 4096

// Bytes parses input, rejects duplicate object keys at every nesting level,
// and emits compact JSON with lexicographically ordered object keys.
func Bytes(input []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("canonical JSON: multiple top-level values")
		}
		return nil, fmt.Errorf("canonical JSON: trailing data: %w", err)
	}
	var out bytes.Buffer
	if err := writeValue(&out, v); err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	return out.Bytes(), nil
}

// HashJSON returns the lowercase SHA-256 hash of canonical JSON bytes.
func HashJSON(input []byte) (string, error) {
	b, err := Bytes(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := make(map[string]any)
			seen := make(map[string]struct{})
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return nil, fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
				value, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				obj[key] = value
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			var array []any
			for dec.More() {
				value, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected delimiter %q", t)
		}
	default:
		return t, nil
	}
}

func writeValue(out *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		if v {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case string:
		encoded, _ := json.Marshal(v)
		out.Write(encoded)
	case json.Number:
		canonical, err := canonicalNumber(string(v))
		if err != nil {
			return err
		}
		out.WriteString(canonical)
	case []any:
		out.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeValue(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := writeValue(out, v[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", value)
	}
	return nil
}

func canonicalNumber(input string) (string, error) {
	sign := ""
	if strings.HasPrefix(input, "-") {
		sign = "-"
		input = input[1:]
	}

	exponent := int64(0)
	if index := strings.IndexAny(input, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(input[index+1:], 10, 64)
		if err != nil {
			return "", fmt.Errorf("number exponent exceeds canonical expansion limit")
		}
		exponent = parsed
		input = input[:index]
	}

	integer, fraction := input, ""
	if index := strings.IndexByte(input, '.'); index >= 0 {
		integer, fraction = input[:index], input[index+1:]
	}
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return "0", nil
	}
	scale := exponent - int64(len(fraction))
	trimmedDigits := strings.TrimRight(digits, "0")
	scale += int64(len(digits) - len(trimmedDigits))
	digits = trimmedDigits

	point := int64(len(digits)) + scale
	if (scale > 0 && point < int64(len(digits))) || (scale < 0 && point > int64(len(digits))) {
		return "", fmt.Errorf("number exponent exceeds canonical expansion limit")
	}
	length, err := expandedNumberLength(len(sign), len(digits), point)
	if err != nil || length > maxExpandedNumberBytes {
		return "", fmt.Errorf("number exceeds canonical expansion limit")
	}

	var out strings.Builder
	out.Grow(length)
	out.WriteString(sign)
	switch {
	case point >= int64(len(digits)):
		out.WriteString(digits)
		out.WriteString(strings.Repeat("0", int(point)-len(digits)))
	case point > 0:
		out.WriteString(digits[:point])
		out.WriteByte('.')
		out.WriteString(digits[point:])
	default:
		out.WriteString("0.")
		out.WriteString(strings.Repeat("0", int(-point)))
		out.WriteString(digits)
	}
	return out.String(), nil
}

func expandedNumberLength(signLength, digitsLength int, point int64) (int, error) {
	var length int64
	switch {
	case point >= int64(digitsLength):
		length = point
	case point > 0:
		length = int64(digitsLength) + 1
	default:
		length = 2 - point + int64(digitsLength)
	}
	length += int64(signLength)
	if length < 0 || length > int64(maxExpandedNumberBytes) {
		return 0, fmt.Errorf("number exceeds canonical expansion limit")
	}
	return int(length), nil
}
