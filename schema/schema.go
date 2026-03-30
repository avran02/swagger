package schema

import (
	"reflect"
	"strings"
	"time"
)

var timeType = reflect.TypeOf(time.Time{})

// Schema represents an OpenAPI 3.0 Schema Object.
type Schema struct {
	Type                 string             `yaml:"type,omitempty" json:"type,omitempty"`
	Format               string             `yaml:"format,omitempty" json:"format,omitempty"`
	Properties           map[string]*Schema `yaml:"properties,omitempty" json:"properties,omitempty"`
	Items                *Schema            `yaml:"items,omitempty" json:"items,omitempty"`
	Required             []string           `yaml:"required,omitempty" json:"required,omitempty"`
	Enum                 []any              `yaml:"enum,omitempty" json:"enum,omitempty"`
	Example              any                `yaml:"example,omitempty" json:"example,omitempty"`
	Default              any                `yaml:"default,omitempty" json:"default,omitempty"`
	Ref                  string             `yaml:"$ref,omitempty" json:"$ref,omitempty"`
	Nullable             bool               `yaml:"nullable,omitempty" json:"nullable,omitempty"`
	AdditionalProperties *Schema            `yaml:"additionalProperties,omitempty" json:"additionalProperties,omitempty"`
	Description          string             `yaml:"description,omitempty" json:"description,omitempty"`
}

// Generator builds OpenAPI schemas from Go types via reflection.
// It accumulates named component schemas so that structs are referenced rather
// than inlined everywhere they appear.
type Generator struct {
	Components map[string]*Schema
}

// NewGenerator creates a ready-to-use Generator.
func NewGenerator() *Generator {
	return &Generator{
		Components: make(map[string]*Schema),
	}
}

// SchemaForType returns either a $ref (for named structs) or an inline Schema
// for primitive/slice/map types. It is the main entry point.
func (g *Generator) SchemaForType(t reflect.Type) *Schema {
	// Unwrap pointer — nullable flag is handled by the caller context.
	nullable := false
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
		nullable = true
	}

	s := g.buildSchema(t)
	if nullable && s != nil {
		s.Nullable = true
	}
	return s
}

func (g *Generator) buildSchema(t reflect.Type) *Schema {
	// Dereference pointer one more level if needed.
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	// time.Time → string date-time
	if t == timeType {
		return &Schema{Type: "string", Format: "date-time"}
	}

	switch t.Kind() {
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return intSchema(t)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer", Format: "int64"}
	case reflect.Float32:
		return &Schema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &Schema{Type: "number", Format: "double"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", Format: "byte"}
		}
		// Unwrap pointer elem before recursing so []*Foo produces items:$ref:Foo,
		// not items:{type:string} from a missed ptr→struct path.
		elemType := t.Elem()
		nullable := false
		if elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
			nullable = true
		}
		itemSchema := g.buildSchema(elemType)
		if nullable && itemSchema.Ref == "" {
			itemSchema.Nullable = true
		}
		return &Schema{Type: "array", Items: itemSchema}
	case reflect.Map:
		return &Schema{Type: "object", AdditionalProperties: g.buildSchema(t.Elem())}
	case reflect.Struct:
		return g.structSchema(t)
	case reflect.Interface:
		return &Schema{}
	default:
		return &Schema{Type: "string"}
	}
}

// structSchema builds a full object schema for a struct type and registers it
// in Components so it can be reused as a $ref.
func (g *Generator) structSchema(t reflect.Type) *Schema {
	name := typeName(t)

	// Return a $ref if already built OR being built (cycle guard).
	if _, exists := g.Components[name]; exists {
		return &Schema{Ref: "#/components/schemas/" + name}
	}

	// Pre-register a placeholder to break cycles BEFORE recursing into fields.
	placeholder := &Schema{Type: "object"}
	g.Components[name] = placeholder

	props := make(map[string]*Schema)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// Flatten anonymous embedded structs into parent.
		if field.Anonymous {
			ft := field.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				embedded := g.structSchemaInline(ft)
				for k, v := range embedded.Properties {
					props[k] = v
				}
				required = append(required, embedded.Required...)
				continue
			}
		}

		jsonTag, ok := field.Tag.Lookup("json")
		if !ok || jsonTag == "-" {
			continue
		}
		jsonName, jsonOpts := splitTag(jsonTag)
		if jsonName == "" {
			jsonName = field.Name
		}

		fs := g.fieldSchema(field)

		// A field is required unless:
		//   • it has omitempty in the json tag
		//   • it is a pointer type (optional by nature)
		//   • it has required:"false" explicitly
		isPointer := field.Type.Kind() == reflect.Ptr
		omitempty := strings.Contains(jsonOpts, "omitempty")
		requiredTag, hasRequiredTag := field.Tag.Lookup("required")
		explicitlyOptional := hasRequiredTag && strings.ToLower(strings.TrimSpace(requiredTag)) == "false"

		if !omitempty && !isPointer && !explicitlyOptional {
			required = append(required, jsonName)
		}

		props[jsonName] = fs
	}

	placeholder.Properties = props
	if len(required) > 0 {
		placeholder.Required = uniqueStrings(required)
	}

	return &Schema{Ref: "#/components/schemas/" + name}
}

// structSchemaInline builds the schema without registering in components —
// used only for flattening anonymous embedded fields.
func (g *Generator) structSchemaInline(t reflect.Type) *Schema {
	props := make(map[string]*Schema)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		jsonTag, ok := field.Tag.Lookup("json")
		if !ok || jsonTag == "-" {
			continue
		}
		jsonName, jsonOpts := splitTag(jsonTag)
		if jsonName == "" {
			jsonName = field.Name
		}
		fs := g.fieldSchema(field)

		isPointer := field.Type.Kind() == reflect.Ptr
		omitempty := strings.Contains(jsonOpts, "omitempty")
		requiredTag, hasRequiredTag := field.Tag.Lookup("required")
		explicitlyOptional := hasRequiredTag && strings.ToLower(strings.TrimSpace(requiredTag)) == "false"

		if !omitempty && !isPointer && !explicitlyOptional {
			required = append(required, jsonName)
		}
		props[jsonName] = fs
	}

	return &Schema{Type: "object", Properties: props, Required: required}
}

// fieldSchema resolves the OpenAPI schema for a single struct field, applying
// all supported tags: example, enum, format, default, description.
func (g *Generator) fieldSchema(field reflect.StructField) *Schema {
	ft := field.Type
	nullable := false
	if ft.Kind() == reflect.Ptr {
		ft = ft.Elem()
		nullable = true
	}

	var s *Schema

	// time.Time special case
	if ft == timeType {
		s = &Schema{Type: "string", Format: "date-time"}
	} else if ft.Kind() == reflect.Struct {
		ref := g.structSchema(ft)
		s = ref
	} else {
		s = g.buildSchema(ft)
	}

	if s == nil {
		s = &Schema{}
	}

	if nullable {
		// Clone so we don't mutate the shared component schema.
		clone := *s
		clone.Nullable = true
		s = &clone
	}

	// Apply tag overrides. When the schema is a bare $ref we must clone first
	// so that per-field annotations don't bleed into the shared component.
	if v, ok := field.Tag.Lookup("example"); ok {
		if s.Ref != "" {
			c := *s
			s = &c
		}
		s.Example = v
	}
	if v, ok := field.Tag.Lookup("default"); ok {
		if s.Ref != "" {
			c := *s
			s = &c
		}
		s.Default = v
	}
	if v, ok := field.Tag.Lookup("format"); ok {
		if s.Ref != "" {
			c := *s
			s = &c
		}
		s.Format = v
	}
	if v, ok := field.Tag.Lookup("enum"); ok {
		if s.Ref != "" {
			c := *s
			s = &c
		}
		for _, e := range strings.Split(v, ",") {
			s.Enum = append(s.Enum, strings.TrimSpace(e))
		}
	}
	if v, ok := field.Tag.Lookup("description"); ok {
		if s.Ref != "" {
			c := *s
			s = &c
		}
		s.Description = v
	}

	return s
}

// ParametersForType returns the OpenAPI Parameter objects derived from the
// query / path / header / cookie tags of the given struct type.
func (g *Generator) ParametersForType(t reflect.Type) []Parameter {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return g.collectParameters(t)
}

func (g *Generator) collectParameters(t reflect.Type) []Parameter {
	var params []Parameter
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		// Flatten anonymous embeds.
		if field.Anonymous {
			ft := field.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				params = append(params, g.collectParameters(ft)...)
				continue
			}
		}

		sources := []struct {
			tag string
			in  string
		}{
			{"query", "query"},
			{"path", "path"},
			{"header", "header"},
			{"cookie", "cookie"},
		}

		for _, src := range sources {
			tag, ok := field.Tag.Lookup(src.tag)
			if !ok {
				continue
			}
			name, _ := splitTag(tag)
			if name == "" || name == "-" {
				continue
			}

			// required resolution:
			//   path params are always required.
			//   others: required unless pointer type, or required:"false" explicitly set.
			isPointer := field.Type.Kind() == reflect.Ptr
			requiredTag, hasRequiredTag := field.Tag.Lookup("required")
			var required bool
			if src.in == "path" {
				required = true
			} else if hasRequiredTag {
				required = strings.ToLower(strings.TrimSpace(requiredTag)) != "false"
			} else {
				required = !isPointer
			}

			paramSchema := g.buildSchema(field.Type)

			// Attach default to the schema when present.
			if defVal, ok := field.Tag.Lookup("default"); ok {
				clone := *paramSchema
				clone.Default = defVal
				paramSchema = &clone
			}

			p := Parameter{
				Name:     name,
				In:       src.in,
				Required: required,
				Schema:   paramSchema,
			}
			if v, ok := field.Tag.Lookup("example"); ok {
				p.Example = v
			}
			if v, ok := field.Tag.Lookup("description"); ok {
				p.Description = v
			}
			params = append(params, p)
		}
	}
	return params
}

// Parameter represents an OpenAPI Parameter Object.
type Parameter struct {
	Name        string  `yaml:"name" json:"name"`
	In          string  `yaml:"in" json:"in"`
	Required    bool    `yaml:"required" json:"required"`
	Description string  `yaml:"description,omitempty" json:"description,omitempty"`
	Example     any     `yaml:"example,omitempty" json:"example,omitempty"`
	Schema      *Schema `yaml:"schema,omitempty" json:"schema,omitempty"`
}

// ---- helpers ----------------------------------------------------------------

func typeName(t reflect.Type) string {
	if t.PkgPath() == "" {
		return t.Name()
	}
	// Use only the last segment of the package path to keep names readable.
	pkg := t.PkgPath()
	if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
		pkg = pkg[idx+1:]
	}
	return pkg + "." + t.Name()
}

func intSchema(t reflect.Type) *Schema {
	switch t.Kind() {
	case reflect.Int32:
		return &Schema{Type: "integer", Format: "int32"}
	default:
		return &Schema{Type: "integer", Format: "int64"}
	}
}

func splitTag(tag string) (string, string) {
	parts := strings.SplitN(tag, ",", 2)
	name := strings.TrimSpace(parts[0])
	opts := ""
	if len(parts) == 2 {
		opts = parts[1]
	}
	return name, opts
}

func uniqueStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := ss[:0]
	for _, s := range ss {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
