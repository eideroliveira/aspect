package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// decodeLenient unmarshals a model's JSON answer into out, coercing values
// whose JSON type does not match the Go type: a bool or number where a
// string is expected, a lone value where a list is expected, "true" where a
// bool is expected. With structured outputs the grammar prevents these; in
// the schema-less fallback the model occasionally writes `key: true` for a
// primary key or a single string for a list, and refusing the whole answer
// for that is worse than repairing it.
func decodeLenient(data []byte, out any) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return describeJSONError(err, data)
	}
	fixed := coerce(raw, reflect.TypeOf(out).Elem(), "")
	b, err := json.Marshal(fixed)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return describeJSONError(err, b)
	}
	return nil
}

// coerce reshapes v to fit t. key is the JSON key v was found under, used
// for the one field-specific rule (a boolean primary-key marker).
func coerce(v any, t reflect.Type, key string) any {
	if v == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		return coerce(v, t.Elem(), key)
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		fields := jsonFields(t)
		out := make(map[string]any, len(m))
		for k, val := range m {
			f, ok := fields[strings.ToLower(k)]
			if !ok {
				continue // additionalProperties: false
			}
			out[k] = coerce(val, f.Type, strings.ToLower(k))
		}
		return out
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return v
		}
		list, ok := v.([]any)
		if !ok {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) == "" {
				return []any{}
			}
			list = []any{v}
		}
		for i := range list {
			list[i] = coerce(list[i], t.Elem(), key)
		}
		return list
	case reflect.Map:
		return v
	case reflect.String:
		switch x := v.(type) {
		case string:
			return x
		case bool:
			if key == "key" {
				if x {
					return "primary"
				}
				return ""
			}
			return strconv.FormatBool(x)
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		default:
			b, _ := json.Marshal(x)
			return string(b)
		}
	case reflect.Bool:
		switch x := v.(type) {
		case bool:
			return x
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "yes", "y", "1", "required", "unique", "primary":
				return true
			default:
				return false
			}
		case float64:
			return x != 0
		}
	case reflect.Float32, reflect.Float64:
		switch x := v.(type) {
		case float64:
			return x
		case string:
			s := strings.TrimSuffix(strings.TrimSpace(x), "%")
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0.0
			}
			if strings.HasSuffix(strings.TrimSpace(x), "%") {
				return f / 100
			}
			return f
		case bool:
			if x {
				return 1.0
			}
			return 0.0
		}
	case reflect.Int, reflect.Int64, reflect.Int32:
		switch x := v.(type) {
		case float64:
			return x
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
			if err != nil {
				return 0
			}
			return f
		case bool:
			if x {
				return 1
			}
			return 0
		}
	}
	return v
}

// jsonFields maps lower-cased JSON keys to struct fields, following
// embedded structs the way encoding/json does.
func jsonFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && (name == "" || strings.Contains(tag, "inline")) {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if name == "" {
				name = f.Name
			}
			out[strings.ToLower(name)] = f
		}
	}
	walk(t)
	return out
}

// describeJSONError points at where a decode failed, so the report shows the
// offending value rather than the start of a long answer.
func describeJSONError(err error, data []byte) error {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	offset := int64(-1)
	switch {
	case errors.As(err, &typeErr):
		offset = typeErr.Offset
	case errors.As(err, &syntaxErr):
		offset = syntaxErr.Offset
	}
	if offset < 0 {
		return fmt.Errorf("agent returned invalid JSON: %w\n--- response ---\n%s", err, truncate(string(data), 2000))
	}
	start := offset - 300
	if start < 0 {
		start = 0
	}
	end := offset + 300
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return fmt.Errorf("agent returned invalid JSON: %w\n--- around byte %d ---\n%s", err, offset, string(data[start:end]))
}
