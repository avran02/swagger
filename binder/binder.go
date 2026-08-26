package binder

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avran02/swagger/codec"
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

		set := compileSetter(field.Type)
		et := errType(field.Type)
		for _, source := range [...]string{"query", "path", "header", "cookie", "form", "multipart"} {
			if tag, ok := field.Tag.Lookup(source); ok {
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
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return err
			}
			fv.SetInt(n)
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(fv reflect.Value, raw string) error {
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return err
			}
			fv.SetUint(n)
			return nil
		}
	case reflect.Float32, reflect.Float64:
		return func(fv reflect.Value, raw string) error {
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return err
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
	verr := &ValidationError{}

	if err := bindBody(r, dst, rv, plan, verr); err != nil {
		verr.Detail = append(verr.Detail, FieldError{
			Loc:  []string{"body"},
			Msg:  err.Error(),
			Type: "json_invalid",
		})
	}

	bindNonBody(r, rv, plan, verr)

	if len(verr.Detail) > 0 {
		return verr
	}
	return nil
}

// bindBody decodes the JSON body into dst when the Content-Type is
// application/json AND the struct has at least one `json` tag.
func bindBody(r *http.Request, dst any, rv reflect.Value, plan *structPlan, verr *ValidationError) error {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		return bindMultipart(r, rv, plan, verr)
	}
	if plan.hasForm {
		for i := range plan.fields {
			f := &plan.fields[i]
			if f.source == "form" && f.required {
				verr.add("body", f.key, "missing", fmt.Errorf("Field required"))
			}
		}
		return nil
	}
	if !plan.hasJSON {
		return nil
	}
	ct := r.Header.Get("Content-Type")
	if r.Body == nil || r.ContentLength == 0 {
		if methodHasBody(r.Method) {
			validateJSONRequired(nil, rv.Type(), nil, verr)
		}
		return nil
	}
	if !strings.Contains(ct, "application/json") {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if err := codec.UnmarshalStrict(data, dst); err != nil {
		return err
	}
	validateJSONRequired(data, rv.Type(), nil, verr)
	return nil
}

// bindNonBody fills query / path / header / cookie fields following the
// precompiled plan, appending every failure to verr.
func bindNonBody(r *http.Request, rv reflect.Value, plan *structPlan, verr *ValidationError) {
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

		if err := f.set(rv.FieldByIndex(f.index), raw); err != nil {
			verr.add(f.source, f.key, f.errType, err)
		}
	}
}

func bindMultipart(r *http.Request, rv reflect.Value, plan *structPlan, verr *ValidationError) error {
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
	var object map[string]json.RawMessage
	if len(data) > 0 && json.Unmarshal(data, &object) != nil {
		return
	}
	validateJSONObject(object, t, prefix, verr)
}

func validateJSONObject(object map[string]json.RawMessage, t reflect.Type, prefix []string, verr *ValidationError) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous {
			validateJSONObject(object, f.Type, prefix, verr)
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		if !ok || tag == "-" {
			continue
		}
		name := parseTagName(tag)
		if name == "" {
			name = f.Name
		}
		_, present := object[name]
		required := runtimeRequired(f, "json") && !strings.Contains(tag, "omitempty")
		if required && !present {
			verr.Detail = append(verr.Detail, FieldError{Loc: append(append([]string{"body"}, prefix...), name), Msg: "Field required", Type: "missing"})
		}
		if present {
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Time{}) {
				var child map[string]json.RawMessage
				if json.Unmarshal(object[name], &child) == nil {
					validateJSONObject(child, ft, append(prefix, name), verr)
				}
			}
		}
	}
}

// hasTag reports whether any field in t (recursively for embeds) carries the
// given tag name.
func hasTag(t reflect.Type, tagName string) bool {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup(tagName); ok {
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
func parseTagName(tag string) string {
	parts := strings.SplitN(tag, ",", 2)
	return strings.TrimSpace(parts[0])
}
