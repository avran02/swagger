package binder

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"

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
	index   []int // field index path into the struct (through embedded structs)
	source  string
	key     string
	def     string // `default` tag value
	errType string
	set     setterFunc
}

// structPlan holds everything BindRequest needs to know about a DTO type,
// computed once via reflection and reused for every request.
type structPlan struct {
	hasJSON bool
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
		for _, source := range [...]string{"query", "path", "header", "cookie"} {
			if tag, ok := field.Tag.Lookup(source); ok {
				p.fields = append(p.fields, fieldPlan{
					index:   idx,
					source:  source,
					key:     parseTagName(tag),
					def:     field.Tag.Get("default"),
					errType: et,
					set:     set,
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

	if err := bindBody(r, dst, plan); err != nil {
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
func bindBody(r *http.Request, dst any, plan *structPlan) error {
	if !plan.hasJSON {
		return nil
	}
	ct := r.Header.Get("Content-Type")
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	if !strings.Contains(ct, "application/json") {
		return nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return codec.UnmarshalStrict(data, dst)
}

// bindNonBody fills query / path / header / cookie fields following the
// precompiled plan, appending every failure to verr.
func bindNonBody(r *http.Request, rv reflect.Value, plan *structPlan, verr *ValidationError) {
	var query url.Values // parsed lazily, at most once per request

	for i := range plan.fields {
		f := &plan.fields[i]

		raw := ""
		switch f.source {
		case "query":
			if query == nil {
				query = r.URL.Query()
			}
			raw = query.Get(f.key)
		case "path":
			if chiURLParam != nil {
				raw = chiURLParam(r, f.key)
			}
		case "header":
			raw = r.Header.Get(f.key)
		case "cookie":
			if c, err := r.Cookie(f.key); err == nil {
				raw = c.Value
			}
		}

		if raw == "" {
			raw = f.def
		}
		if raw == "" {
			continue
		}

		if err := f.set(rv.FieldByIndex(f.index), raw); err != nil {
			verr.add(f.source, f.key, f.errType, err)
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
