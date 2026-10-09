package binder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avran02/swagger/codec"
	"github.com/avran02/swagger/schema"
)

// chiURLParam is the function signature compatible with chi's URLParam.
// We use a function variable so the binder has zero hard dependency on chi —
// callers inject it once at startup via SetURLParamFunc.
var chiURLParam func(r *http.Request, key string) string

// SetURLParamFunc injects the chi.URLParam function (or any compatible
// path-param extractor) into the binder. Call this once in main/init:
//
//	binder.SetURLParamFunc(chi.URLParam)
func SetURLParamFunc(fn func(r *http.Request, key string) string) {
	chiURLParam = fn
}

// FieldError describes a single binding/validation failure in the same shape
// FastAPI uses: loc identifies the source and parameter, e.g. ["query","page"]
// or ["body"].
type FieldError struct {
	Loc  []string `json:"loc"`
	Msg  string   `json:"msg"`
	Type string   `json:"type"`
}

// ValidationError aggregates every binding failure of a request. It is the
// error type returned by BindRequest and serialises to the FastAPI-style
// {"detail": [...]} JSON body.
type ValidationError struct {
	Detail []FieldError `json:"detail"`
}

func (e *ValidationError) Error() string {
	msgs := make([]string, len(e.Detail))
	for i, d := range e.Detail {
		msgs[i] = strings.Join(d.Loc, ".") + ": " + d.Msg
	}
	return "binder: " + strings.Join(msgs, "; ")
}

func (e *ValidationError) add(in, name, errType string, err error) {
	e.Detail = append(e.Detail, FieldError{
		Loc:  []string{in, name},
		Msg:  err.Error(),
		Type: errType,
	})
}

// errType maps a field's Go type to a FastAPI-style error type token.
func errType(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool:
		return "bool_parsing"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "int_parsing"
	case reflect.Float32, reflect.Float64:
		return "float_parsing"
	default:
		return "value_error"
	}
}

// fieldPlan is the precompiled binding recipe for one (field, source) pair:
// where to read the raw value from, which struct field to set, and how.
type fieldPlan struct {
	index    []int // field index path into the struct (through embedded structs)
	source   string
	key      string
	def      string // `default` tag value
	errType  string
	set      setterFunc
	required bool
	file     bool
}

// structPlan holds everything BindRequest needs to know about a DTO type,
// computed once via reflection and reused for every request.
type structPlan struct {
	err     error
	hasJSON bool
	hasForm bool
	fields  []fieldPlan
}

type setterFunc func(fv reflect.Value, raw string) error

// planCache maps reflect.Type → *structPlan.
var planCache sync.Map

// Precompile builds and caches the binding plan for dto's type so the first
// real request pays no reflection cost. Called automatically for every
// RequestDTO at registration time; non-struct values are ignored.
func Precompile(dto any) {
	if dto == nil {
		return
	}
	t := reflect.TypeOf(dto)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	planFor(t)
}

// planFor returns the cached plan for t, compiling it on first use.
func planFor(t reflect.Type) *structPlan {
	if p, ok := planCache.Load(t); ok {
		return p.(*structPlan)
	}
	p := compile(t)
	actual, _ := planCache.LoadOrStore(t, p)
	return actual.(*structPlan)
}

func compile(t reflect.Type) *structPlan {
	p := &structPlan{hasJSON: hasTag(t, "json")}
	compileFields(t, nil, p)
	if p.hasJSON && p.hasForm && p.err == nil {
		p.err = fmt.Errorf("binder: DTO must not mix JSON and multipart body fields")
	}
	return p
}

// compileFields walks t's fields (recursing into embedded value structs) and
// appends a fieldPlan for every query/path/header/cookie tag it finds.
func compileFields(t reflect.Type, index []int, p *structPlan) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" { // unexported — not settable
			continue
		}

		idx := make([]int, len(index)+1)
		copy(idx, index)
		idx[len(index)] = i

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			compileFields(field.Type, idx, p)
			continue
		}

		sources := 0
		if tag, ok := field.Tag.Lookup("json"); ok && parseTagName(tag) != "-" {
			sources++
		}
		set := compileSetter(field.Type)
		et := errType(field.Type)
		for _, source := range [...]string{"query", "path", "header", "cookie", "form", "multipart"} {
			if tag, ok := field.Tag.Lookup(source); ok {
				key := parseTagName(tag)
				if key == "-" {
					continue
				}
				if key == "" {
					p.err = fmt.Errorf("binder: field %s has an empty %s tag", field.Name, source)
					continue
				}
				sources++
				if sources > 1 {
					p.err = fmt.Errorf("binder: field %s must declare only one binding source", field.Name)
				}
				bindingSource := source
				if source == "multipart" {
					bindingSource = "form"
				}
				if bindingSource == "form" {
					p.hasForm = true
				}
				p.fields = append(p.fields, fieldPlan{
					index:    idx,
					source:   bindingSource,
					key:      parseTagName(tag),
					def:      field.Tag.Get("default"),
					errType:  et,
					set:      set,
					required: runtimeRequired(field, bindingSource),
					file:     isFileField(field.Type),
				})
			}
		}
	}
}

// compileSetter picks the string→value conversion for t once, so per-request
// binding does no kind switching.
func compileSetter(t reflect.Type) setterFunc {
	if t.Kind() == reflect.Ptr {
		inner := compileSetter(t.Elem())
		elem := t.Elem()
		return func(fv reflect.Value, raw string) error {
			if fv.IsNil() {
				fv.Set(reflect.New(elem))
			}
			return inner(fv.Elem(), raw)
		}
	}

	switch t.Kind() {
	case reflect.String:
		return func(fv reflect.Value, raw string) error {
			fv.SetString(raw)
			return nil
		}
	case reflect.Bool:
		return func(fv reflect.Value, raw string) error {
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return err
			}
			fv.SetBool(b)
			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(fv reflect.Value, raw string) error {
			n, err := strconv.ParseInt(raw, 10, t.Bits())
			if err != nil {
				return err
			}
			fv.SetInt(n)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(fv reflect.Value, raw string) error {
			n, err := strconv.ParseUint(raw, 10, t.Bits())
			if err != nil {
				return err
			}
			fv.SetUint(n)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		return func(fv reflect.Value, raw string) error {
			n, err := strconv.ParseFloat(raw, t.Bits())
			if err != nil {
				return err
			}
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return fmt.Errorf("non-finite number is not allowed")
			}
			fv.SetFloat(n)
			return nil
		}
	default:
		kind := t.Kind()
		return func(reflect.Value, string) error {
			return fmt.Errorf("unsupported kind %s", kind)
		}
	}
}

// BindRequest populates dst from the incoming HTTP request using struct field tags:
//
//	json    – JSON request body (application/json)
//	query   – URL query parameters
//	path    – chi URL path parameters
//	header  – HTTP headers
//	cookie  – HTTP cookies
//
// dst must be a non-nil pointer to a struct.
//
// All binding failures are collected (not just the first one) and returned as
// a *ValidationError.
//
// The reflection walk over dst's type happens once per type — the resulting
// binding plan is cached, so subsequent calls only pay for value conversion.
func BindRequest(r *http.Request, dst any) error {
	return bindRequest(r, dst, false, nil)
}

func bindRequest(r *http.Request, dst any, validateEnums bool, options []schema.Option) error {
	if dst == nil {
		return fmt.Errorf("binder: dst must not be nil")
	}

	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("binder: dst must be a non-nil pointer")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("binder: dst must point to a struct")
	}

	plan := planFor(rv.Type())
	if plan.err != nil {
		return plan.err
	}
	verr := &ValidationError{}
	var enums *enumValidation
	if validateEnums {
		enums = enumValidationFor(rv.Type(), options)
		if enums.err != nil {
			return enums.err
		}
	}

	if err := bindBody(r, dst, rv, plan, verr, enums); err != nil {
		verr.Detail = append(verr.Detail, FieldError{
			Loc:  []string{"body"},
			Msg:  err.Error(),
			Type: "json_invalid",
		})
	}

	bindNonBody(r, rv, plan, verr, enums)

	if len(verr.Detail) > 0 {
		return verr
	}
	return nil
}

// bindBody decodes the JSON body into dst when the Content-Type is
// application/json AND the struct has at least one `json` tag.
func bindBody(r *http.Request, dst any, rv reflect.Value, plan *structPlan, verr *ValidationError, enums *enumValidation) error {
	if !plan.hasJSON && !plan.hasForm {
		return nil
	}
	if r.Body == nil || r.ContentLength == 0 {
		if plan.hasForm {
			for _, f := range plan.fields {
				if f.source == "form" && f.required {
					verr.add("body", f.key, "missing", fmt.Errorf("Field required"))
				}
			}
		} else if methodHasBody(r.Method) {
			validateJSONRequired(nil, rv.Type(), nil, verr)
		}
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return fmt.Errorf("invalid Content-Type: %w", err)
	}
	if plan.hasForm {
		if mediaType != "multipart/form-data" {
			return fmt.Errorf("expected Content-Type multipart/form-data")
		}
		return bindMultipart(r, rv, plan, verr, enums)
	}
	if mediaType != "application/json" {
		return fmt.Errorf("expected Content-Type application/json")
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	// Validate the complete input and its allowed field sources before decoding
	// into dst. A JSON decoder alone also accepts exported non-body fields.
	validateJSONRequired(data, rv.Type(), nil, verr)
	if len(verr.Detail) > 0 {
		return nil
	}
	if err := codec.UnmarshalStrict(data, dst); err != nil {
		return err
	}
	if enums != nil {
		enums.json(data, rv, enums.body, []string{"body"}, verr)
	}
	return nil
}

// bindNonBody fills query / path / header / cookie fields following the
// precompiled plan, appending every failure to verr.
func bindNonBody(r *http.Request, rv reflect.Value, plan *structPlan, verr *ValidationError, enums *enumValidation) {
	var query url.Values // parsed lazily, at most once per request

	for i := range plan.fields {
		f := &plan.fields[i]
		if f.source == "form" {
			continue
		}

		raw := ""
		present := false
		switch f.source {
		case "query":
			if query == nil {
				query = r.URL.Query()
			}
			values, ok := query[f.key]
			present = ok && len(values) > 0
			if present {
				raw = values[0]
			}
		case "path":
			if chiURLParam != nil {
				raw = chiURLParam(r, f.key)
				present = raw != ""
			}
		case "header":
			values, ok := r.Header[http.CanonicalHeaderKey(f.key)]
			present = ok && len(values) > 0
			if present {
				raw = values[0]
			}
		case "cookie":
			if c, err := r.Cookie(f.key); err == nil {
				raw = c.Value
				present = true
			}
		}

		if !present && f.def != "" {
			raw = f.def
			present = true
		}
		if !present {
			if f.source == "path" && chiURLParam == nil {
				continue
			}
			if f.required {
				verr.add(f.source, f.key, "missing", fmt.Errorf("Field required"))
			}
			continue
		}

		fv := rv.FieldByIndex(f.index)
		if err := f.set(fv, raw); err != nil {
			verr.add(f.source, f.key, f.errType, err)
		} else if enums != nil {
			enums.parameter(fv, f, verr)
		}
	}
}

func bindMultipart(r *http.Request, rv reflect.Value, plan *structPlan, verr *ValidationError, enums *enumValidation) error {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return err
	}
	for i := range plan.fields {
		f := &plan.fields[i]
		if f.source != "form" {
			continue
		}
		fv := rv.FieldByIndex(f.index)
		if f.file {
			files := r.MultipartForm.File[f.key]
			if len(files) == 0 {
				if f.required {
					verr.add("body", f.key, "missing", fmt.Errorf("Field required"))
				}
				continue
			}
			setFiles(fv, files)
			continue
		}
		values, present := r.MultipartForm.Value[f.key]
		raw := ""
		if present && len(values) > 0 {
			raw = values[0]
		} else {
			present = false
		}
		if !present && f.def != "" {
			raw, present = f.def, true
		}
		if !present {
			if f.required {
				verr.add("body", f.key, "missing", fmt.Errorf("Field required"))
			}
			continue
		}
		if err := f.set(fv, raw); err != nil {
			verr.add("body", f.key, f.errType, err)
		} else if enums != nil {
			enums.parameter(fv, f, verr)
		}
	}
	return nil
}

func isFileField(t reflect.Type) bool {
	if t == reflect.PointerTo(multipartFileHeaderType) {
		return true
	}
	return t.Kind() == reflect.Slice && t.Elem() == reflect.PointerTo(multipartFileHeaderType)
}

var multipartFileHeaderType = reflect.TypeOf(multipart.FileHeader{})

func setFiles(dst reflect.Value, files []*multipart.FileHeader) {
	if dst.Kind() == reflect.Ptr {
		dst.Set(reflect.ValueOf(files[0]))
		return
	}
	dst.Set(reflect.ValueOf(files))
}

func runtimeRequired(field reflect.StructField, source string) bool {
	if source == "path" {
		return true
	}
	if v, ok := field.Tag.Lookup("required"); ok {
		return !strings.EqualFold(strings.TrimSpace(v), "false")
	}
	if source == "form" {
		return field.Type.Kind() != reflect.Ptr && field.Type.Kind() != reflect.Slice
	}
	return field.Type.Kind() != reflect.Ptr
}

func methodHasBody(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	}
	return false
}

func validateJSONRequired(data []byte, t reflect.Type, prefix []string, verr *ValidationError) {
	if len(data) == 0 {
		data = []byte("{}")
	}
	if !json.Valid(data) {
		verr.Detail = append(verr.Detail, FieldError{Loc: []string{"body"}, Msg: "Expected exactly one valid JSON value", Type: "json_invalid"})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // inspecting keys must not impose float64 limits on JSON numbers
	if err := rejectDuplicateJSONKeys(dec); err != nil {
		bodyError(verr, prefix, "json_invalid", err.Error())
		return
	}
	validateJSONValue(data, t, prefix, verr)
}

// Repeated object keys can make the validation view (last value wins)
// differ from decoding into Go structs (nested objects may be merged).
func rejectDuplicateJSONKeys(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name := key.(string) // syntax was checked with json.Valid
			if seen[name] {
				return fmt.Errorf("Duplicate JSON field %q", name)
			}
			seen[name] = true
		}
		if err := rejectDuplicateJSONKeys(dec); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func bodyError(verr *ValidationError, path []string, kind, message string) {
	loc := append([]string{"body"}, path...)
	verr.Detail = append(verr.Detail, FieldError{Loc: loc, Msg: message, Type: kind})
}

func childPath(prefix []string, name string) []string {
	return append(append([]string(nil), prefix...), name)
}

// jsonFields deliberately includes only explicitly tagged body fields. This
// keeps external binding sources inaccessible to the body, including aliases
// that encoding/json would otherwise accept by Go name or case folding.
func jsonFields(t reflect.Type, fields map[string]reflect.StructField, visiting map[reflect.Type]bool) {
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
		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			jsonFields(ft, fields, visiting)
			continue
		}
		if !tagged {
			continue
		}
		if name == "" {
			name = f.Name
		}
		fields[name] = f
	}
}

func validateJSONValue(data []byte, t reflect.Type, prefix []string, verr *ValidationError) {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		if t.Kind() != reflect.Ptr && t.Kind() != reflect.Interface {
			bodyError(verr, prefix, "null_not_allowed", "Null is not allowed")
		}
		return
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			bodyError(verr, prefix, "object_type", "Expected a JSON object")
			return
		}
		fields := make(map[string]reflect.StructField)
		jsonFields(t, fields, make(map[reflect.Type]bool))
		names := make([]string, 0, len(fields))
		for name := range fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			f := fields[name]
			raw, present := object[name]
			required := runtimeRequired(f, "json") && !jsonTagOption(f.Tag.Get("json"), "omitempty")
			if !present {
				if required {
					bodyError(verr, childPath(prefix, name), "missing", "Field required")
				}
				continue
			}
			validateJSONValue(raw, f.Type, childPath(prefix, name), verr)
		}
		var unknown []string
		for name := range object {
			if _, ok := fields[name]; !ok {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)
		for _, name := range unknown {
			bodyError(verr, childPath(prefix, name), "extra_forbidden", "Unknown body field")
		}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return
		}
		var items []json.RawMessage
		if json.Unmarshal(data, &items) != nil {
			return
		} // decoder reports type errors
		for i, item := range items {
			validateJSONValue(item, t.Elem(), childPath(prefix, strconv.Itoa(i)), verr)
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
			validateJSONValue(items[key], t.Elem(), childPath(prefix, key), verr)
		}
	}
}

// hasTag reports whether any field in t (recursively for embeds) carries the
// given tag name.
func hasTag(t reflect.Type, tagName string) bool {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if tag, ok := f.Tag.Lookup(tagName); ok && f.IsExported() && parseTagName(tag) != "-" {
			return true
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if hasTag(f.Type, tagName) {
				return true
			}
		}
	}
	return false
}

// parseTagName extracts the name part of a "name,option1,option2" tag value.
func jsonTagOption(tag, option string) bool {
	parts := strings.Split(tag, ",")
	for _, value := range parts[1:] {
		if value == option {
			return true
		}
	}
	return false
}

func parseTagName(tag string) string {
	parts := strings.SplitN(tag, ",", 2)
	return strings.TrimSpace(parts[0])
}
