package schema

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

// EnumValues is implemented by scalar types that declare their allowed values.
// Implement it on a value receiver where possible. The generator calls it on a
// zero value (or a non-nil pointer to one), so it must not depend on receiver state.
// Values must match the type's underlying scalar kind. These definitions are
// used by schema generation and BindAndValidate / BindOr422 validation.
//
//	type Status string
//	func (Status) EnumValues() []any { return []any{Draft, Published} }
type EnumValues interface {
	EnumValues() []any
}

// EnumScalar lists the Go scalar types supported by enum schemas.
type EnumScalar interface {
	~string | ~bool | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// Option configures one Generator. Options do not change global state.
type Option func(*Generator)

// WithEnum declares values for a scalar type, including a type from another
// package on which methods cannot be added. It takes precedence over EnumValues.
// An enum tag on a field takes precedence over both. Repeated registrations for
// a type use the last option. An empty or invalid definition is reported by Err.
func WithEnum[T EnumScalar](values ...T) Option {
	t := reflect.TypeOf((*T)(nil)).Elem()
	snapshot := make([]any, len(values))
	for i, value := range values {
		snapshot[i] = value
	}
	return func(g *Generator) { g.enums[t] = enumDefinition{values: append([]any(nil), snapshot...)} }
}

type enumDefinition struct {
	values   []any
	resolved bool
}

// Err reports invalid enum definitions encountered while generating schemas.
// Build propagates this error instead of publishing an incorrect specification.
func (g *Generator) Err() error { return errors.Join(g.enumErrors...) }

func (g *Generator) typeEnum(t reflect.Type, s *Schema) {
	if g.enums == nil {
		g.enums = make(map[reflect.Type]enumDefinition)
	}
	definition, registered := g.enums[t]
	if registered && definition.resolved {
		s.Enum = append([]any(nil), definition.values...)
		return
	}
	if !registered {
		provider, ok := reflect.New(t).Elem().Interface().(EnumValues)
		if !ok {
			provider, ok = reflect.New(t).Interface().(EnumValues)
		}
		if !ok {
			g.enums[t] = enumDefinition{resolved: true}
			return
		}
		definition.values = provider.EnumValues()
	}
	g.enums[t] = enumDefinition{resolved: true}
	if s.Type != "string" && s.Type != "integer" && s.Type != "number" && s.Type != "boolean" {
		g.enumErrors = append(g.enumErrors, fmt.Errorf("enum %s: only scalar types are supported", t))
		return
	}
	values, err := normalizeEnum(t, definition.values)
	if err != nil {
		g.enumErrors = append(g.enumErrors, fmt.Errorf("enum %s: %w", t, err))
		return
	}
	g.enums[t] = enumDefinition{values: values, resolved: true}
	s.Enum = append([]any(nil), values...)
}

func normalizeEnum(t reflect.Type, values []any) ([]any, error) {
	if len(values) == 0 {
		return nil, errors.New("at least one value is required")
	}
	result := make([]any, 0, len(values))
	seen := make(map[any]bool, len(values))
	for i, value := range values {
		v := reflect.ValueOf(value)
		if !v.IsValid() {
			return nil, fmt.Errorf("value %d: null is inferred from pointer fields; omit it from the definition", i)
		}
		var normalized any
		var err error
		switch t.Kind() {
		case reflect.String:
			if v.Kind() != reflect.String {
				return nil, fmt.Errorf("value %d: expected a string", i)
			}
			normalized = v.String()
		case reflect.Bool:
			if v.Kind() != reflect.Bool {
				return nil, fmt.Errorf("value %d: expected a boolean", i)
			}
			normalized = v.Bool()
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if v.Kind() < reflect.Int || v.Kind() > reflect.Int64 {
				return nil, fmt.Errorf("value %d: expected a signed integer", i)
			}
			normalized, err = strconv.ParseInt(strconv.FormatInt(v.Int(), 10), 10, t.Bits())
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v.Kind() < reflect.Uint || v.Kind() > reflect.Uint64 {
				return nil, fmt.Errorf("value %d: expected an unsigned integer", i)
			}
			normalized, err = strconv.ParseUint(strconv.FormatUint(v.Uint(), 10), 10, t.Bits())
		case reflect.Float32, reflect.Float64:
			if v.Kind() != reflect.Float32 && v.Kind() != reflect.Float64 {
				return nil, fmt.Errorf("value %d: expected a floating-point number", i)
			}
			number := v.Float()
			if math.IsNaN(number) || math.IsInf(number, 0) {
				return nil, fmt.Errorf("value %d: a finite number is required", i)
			}
			normalized, err = strconv.ParseFloat(strconv.FormatFloat(number, 'g', -1, 64), t.Bits())
		default:
			return nil, errors.New("only scalar types are supported")
		}
		if err != nil {
			return nil, fmt.Errorf("value %d: out of range for %s", i, t)
		}
		if !seen[normalized] {
			result = append(result, normalized)
			seen[normalized] = true
		}
	}
	return result, nil
}

func nullableSchema(s *Schema) *Schema {
	s = overrideTarget(s)
	s.Nullable = true
	if len(s.Enum) > 0 {
		for _, value := range s.Enum {
			if value == nil {
				return s
			}
		}
		s.Enum = append(s.Enum, nil)
	}
	return s
}
