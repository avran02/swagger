package builder

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// MarshalYAML serializes the OpenAPISpec to YAML bytes with deterministic key
// ordering. We implement a lightweight custom marshaller rather than pulling in
// a full YAML library so the package has zero non-stdlib dependencies.
func MarshalYAML(spec *OpenAPISpec) ([]byte, error) {
	var sb strings.Builder
	if err := marshalValue(&sb, reflect.ValueOf(spec), 0); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

func marshalValue(sb *strings.Builder, v reflect.Value, indent int) error {
	// Dereference pointers
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			sb.WriteString("null")
			return nil
		}
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Struct:
		return marshalStruct(sb, v, indent)
	case reflect.Map:
		return marshalMap(sb, v, indent)
	case reflect.Slice:
		return marshalSlice(sb, v, indent)
	case reflect.String:
		sb.WriteString(quoteString(v.String()))
	case reflect.Bool:
		if v.Bool() {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		sb.WriteString(fmt.Sprintf("%d", v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sb.WriteString(fmt.Sprintf("%d", v.Uint()))
	case reflect.Float32, reflect.Float64:
		sb.WriteString(fmt.Sprintf("%g", v.Float()))
	case reflect.Invalid:
		sb.WriteString("null")
	default:
		sb.WriteString(fmt.Sprintf("%v", v.Interface()))
	}
	return nil
}

func marshalStruct(sb *strings.Builder, v reflect.Value, indent int) error {
	t := v.Type()
	wrote := false
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		fv := v.Field(i)

		yamlTag := ft.Tag.Get("yaml")
		if yamlTag == "" {
			continue
		}
		parts := strings.SplitN(yamlTag, ",", 2)
		key := parts[0]
		omitempty := len(parts) > 1 && strings.Contains(parts[1], "omitempty")

		if key == "-" {
			continue
		}
		if omitempty && isZero(fv) {
			continue
		}

		pad := strings.Repeat("  ", indent)
		sb.WriteString(pad + key + ":")

		if err := marshalInlineOrBlock(sb, fv, indent); err != nil {
			return err
		}
		wrote = true
	}
	if !wrote && indent > 0 {
		// empty object — write nothing extra; the key: was already written by caller
	}
	return nil
}

func marshalMap(sb *strings.Builder, v reflect.Value, indent int) error {
	if v.IsNil() || v.Len() == 0 {
		return nil
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, v.Len())
	for _, k := range v.MapKeys() {
		keys = append(keys, fmt.Sprintf("%v", k.Interface()))
	}
	sort.Strings(keys)

	for _, k := range keys {
		mv := v.MapIndex(reflect.ValueOf(k))
		pad := strings.Repeat("  ", indent)
		sb.WriteString("\n" + pad + k + ":")
		if err := marshalInlineOrBlock(sb, mv, indent); err != nil {
			return err
		}
	}
	return nil
}

func marshalSlice(sb *strings.Builder, v reflect.Value, indent int) error {
	if v.IsNil() || v.Len() == 0 {
		return nil
	}
	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		pad := strings.Repeat("  ", indent)
		sb.WriteString("\n" + pad + "- ")

		// For primitive types, inline. For structs/maps use block.
		actual := elem
		for actual.Kind() == reflect.Ptr || actual.Kind() == reflect.Interface {
			if actual.IsNil() {
				break
			}
			actual = actual.Elem()
		}
		if actual.Kind() == reflect.Struct || actual.Kind() == reflect.Map {
			sb.WriteString("\n")
			if err := marshalValue(sb, elem, indent+2); err != nil {
				return err
			}
		} else {
			if err := marshalValue(sb, elem, indent+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// marshalInlineOrBlock decides whether to put the value on the same line or on
// subsequent indented lines.
func marshalInlineOrBlock(sb *strings.Builder, v reflect.Value, indent int) error {
	// Dereference
	actual := v
	for actual.Kind() == reflect.Ptr || actual.Kind() == reflect.Interface {
		if actual.IsNil() {
			sb.WriteString(" null\n")
			return nil
		}
		actual = actual.Elem()
	}

	switch actual.Kind() {
	case reflect.Struct, reflect.Map:
		if actual.Kind() == reflect.Map && (actual.IsNil() || actual.Len() == 0) {
			sb.WriteString(" {}\n")
			return nil
		}
		if actual.Kind() == reflect.Struct && isZero(actual) {
			sb.WriteString("\n")
			return nil
		}
		sb.WriteString("\n")
		return marshalValue(sb, v, indent+1)
	case reflect.Slice:
		if actual.IsNil() || actual.Len() == 0 {
			sb.WriteString(" []\n")
			return nil
		}
		// Check if slice of primitives → inline
		if isPrimitiveSlice(actual) {
			sb.WriteString(" [")
			for i := 0; i < actual.Len(); i++ {
				if i > 0 {
					sb.WriteString(", ")
				}
				marshalValue(sb, actual.Index(i), 0) //nolint
			}
			sb.WriteString("]\n")
			return nil
		}
		sb.WriteString("\n")
		return marshalSlice(sb, actual, indent+1)
	default:
		sb.WriteString(" ")
		if err := marshalValue(sb, v, indent+1); err != nil {
			return err
		}
		sb.WriteString("\n")
	}
	return nil
}

func isPrimitiveSlice(v reflect.Value) bool {
	if v.Len() == 0 {
		return true
	}
	switch v.Index(0).Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	case reflect.Interface:
		// any / interface{} — check actual value
		elem := v.Index(0).Elem()
		switch elem.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int64, reflect.Float64:
			return true
		}
	}
	return false
}

// quoteString wraps s in double quotes if it contains special YAML chars or is empty.
func quoteString(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := false
	specials := []string{":", "#", "{", "}", "[", "]", ",", "&", "*", "?", "|", "-", "<", ">", "=", "!", "%", "@", "`", "'", "\"", "\n", "\r", "\t"}
	for _, c := range specials {
		if strings.Contains(s, c) {
			needsQuote = true
			break
		}
	}
	// YAML booleans / null / numbers must be quoted when used as strings.
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "null", "~":
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// isZero reports whether a reflect.Value is the zero value for its type.
func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	case reflect.Struct:
		return v.IsZero()
	case reflect.String:
		return v.String() == ""
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	}
	return false
}
