package binder

import (
	"encoding"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/avran02/swagger/schema"
)

// EnumDefinitionError reports invalid enum metadata, rather than invalid input.
// BindOr422 returns 500 for this application configuration error.
type EnumDefinitionError struct{ Cause error }

func (e *EnumDefinitionError) Error() string {
	return "binder: invalid enum definition: " + e.Cause.Error()
}
func (e *EnumDefinitionError) Unwrap() error { return e.Cause }

type enumValidation struct {
	generator  *schema.Generator
	body       *schema.Schema
	form       *schema.Schema
	parameters map[string]*schema.Schema
	err        error
}

var enumValidationCache sync.Map

func enumValidationFor(t reflect.Type, options []schema.Option) *enumValidation {
	if len(options) == 0 {
		if cached, ok := enumValidationCache.Load(t); ok {
			return cached.(*enumValidation)
		}
	}
	g := schema.NewGenerator(options...)
	v := &enumValidation{generator: g, body: g.SchemaForType(t), form: g.MultipartSchemaForType(t), parameters: make(map[string]*schema.Schema)}
	for _, p := range g.ParametersForType(t) {
		v.parameters[p.In+"\x00"+p.Name] = p.Schema
	}
	if err := g.Err(); err != nil {
		v.err = &EnumDefinitionError{Cause: err}
	}
	if len(options) == 0 {
		cached, _ := enumValidationCache.LoadOrStore(t, v)
		return cached.(*enumValidation)
	}
	return v
}

func (v *enumValidation) field(value reflect.Value, s *schema.Schema, loc []string, errors *ValidationError) {
	if s == nil || !value.IsValid() {
		return
	}
	for value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if len(s.Enum) == 0 {
		return
	}
	var actual any
	switch value.Kind() {
	case reflect.String:
		actual = value.String()
	case reflect.Bool:
		actual = value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		actual = value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		actual = value.Uint()
	case reflect.Float32, reflect.Float64:
		actual = value.Float()
	default:
		return
	}
	for _, allowed := range s.Enum {
		if actual == allowed {
			return
		}
	}
	errors.Detail = append(errors.Detail, FieldError{Loc: loc, Msg: "Value is not one of the allowed enum values", Type: "enum"})
}

func (v *enumValidation) parameter(value reflect.Value, f *fieldPlan, errors *ValidationError) {
	s := v.parameters[f.source+"\x00"+f.key]
	source := f.source
	if source == "form" {
		s = v.form.Properties[f.key]
		source = "body"
	}
	v.field(value, s, []string{source, f.key}, errors)
}

func (v *enumValidation) resolve(s *schema.Schema) *schema.Schema {
	if s == nil {
		return nil
	}
	if s.Ref != "" {
		return v.generator.Components[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	if len(s.AllOf) == 1 {
		return v.resolve(s.AllOf[0])
	}
	return s
}

// json validates decoded values but follows the input's presence map. Omitted
// optional fields must not be rejected for having Go's zero value.
func (v *enumValidation) json(data []byte, value reflect.Value, s *schema.Schema, loc []string, errors *ValidationError) {
	if !value.IsValid() || s == nil {
		return
	}
	for value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	v.field(value, s, loc, errors)
	s = v.resolve(s)
	if s == nil {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil {
			return
		}
		fields := enumJSONFieldsFor(value.Type())
		names := make([]string, 0, len(object))
		for name := range object {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			index, ok := fields[name]
			if !ok {
				continue
			}
			child, err := value.FieldByIndexErr(index)
			if err != nil {
				continue
			}
			v.json(object[name], child, s.Properties[name], childPath(loc, name), errors)
		}
	case reflect.Slice, reflect.Array:
		if s.Items == nil {
			return
		}
		var items []json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return
		}
		for i, item := range items {
			if i >= value.Len() {
				break
			}
			v.json(item, value.Index(i), s.Items, childPath(loc, strconv.Itoa(i)), errors)
		}
	case reflect.Map:
		var items map[string]json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return
		}
		keys := make([]string, 0, len(items))
		for key := range items {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			mapKey, ok := enumJSONMapKey(key, value.Type().Key())
			if !ok {
				continue
			}
			v.json(items[key], value.MapIndex(mapKey), s.AdditionalProperties, childPath(loc, key), errors)
		}
	}
}

// Use JSON's map-key conversion, including integer and TextUnmarshaler keys.
func enumJSONMapKey(key string, t reflect.Type) (reflect.Value, bool) {
	if t.Kind() == reflect.String && !reflect.PointerTo(t).Implements(reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()) {
		return reflect.ValueOf(key).Convert(t), true
	}
	encoded, _ := json.Marshal(key)
	data := append([]byte{'{'}, encoded...)
	data = append(data, []byte(":null}")...)
	m := reflect.New(reflect.MapOf(t, reflect.TypeOf(json.RawMessage{})))
	if json.Unmarshal(data, m.Interface()) != nil || m.Elem().Len() != 1 {
		return reflect.Value{}, false
	}
	return m.Elem().MapKeys()[0], true
}

var enumJSONFieldsCache sync.Map

func enumJSONFieldsFor(t reflect.Type) map[string][]int {
	if cached, ok := enumJSONFieldsCache.Load(t); ok {
		return cached.(map[string][]int)
	}
	fields := make(map[string][]int)
	collectEnumJSONFields(t, nil, fields, make(map[reflect.Type]bool))
	cached, _ := enumJSONFieldsCache.LoadOrStore(t, fields)
	return cached.(map[string][]int)
}

func collectEnumJSONFields(t reflect.Type, index []int, fields map[string][]int, visiting map[reflect.Type]bool) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, tagged := f.Tag.Lookup("json")
		name := parseTagName(tag)
		if name == "-" {
			continue
		}
		idx := append(append([]int(nil), index...), i)
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			collectEnumJSONFields(ft, idx, fields, visiting)
			continue
		}
		if !tagged {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = idx
	}
}

// BindAndValidate binds a request and validates enum tags and EnumValues type
// metadata. Options have the same precedence as schema generation and affect
// only this call. BindRequest retains its original binding-only behavior.
func BindAndValidate(r *http.Request, dst any, options ...schema.Option) error {
	return bindRequest(r, dst, true, options)
}
