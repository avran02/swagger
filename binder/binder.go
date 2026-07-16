package binder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
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

func (e *ValidationError) add(in, name string, fieldType reflect.Type, err error) {
	e.Detail = append(e.Detail, FieldError{
		Loc:  []string{in, name},
		Msg:  err.Error(),
		Type: errType(fieldType),
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

	verr := &ValidationError{}

	if err := bindBody(r, dst, rv); err != nil {
		verr.Detail = append(verr.Detail, FieldError{
			Loc:  []string{"body"},
			Msg:  err.Error(),
			Type: "json_invalid",
		})
	}

	bindNonBody(r, rv, verr)

	if len(verr.Detail) > 0 {
		return verr
	}
	return nil
}

// bindBody decodes the JSON body into dst when the Content-Type is
// application/json AND the struct has at least one `json` tag.
func bindBody(r *http.Request, dst any, rv reflect.Value) error {
	if !hasTag(rv.Type(), "json") {
		return nil
	}
	ct := r.Header.Get("Content-Type")
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	if !strings.Contains(ct, "application/json") {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// bindNonBody fills query / path / header / cookie fields via reflect,
// appending every failure to verr.
func bindNonBody(r *http.Request, rv reflect.Value, verr *ValidationError) {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fv := rv.Field(i)

		if !fv.CanSet() {
			continue
		}

		// Recurse into embedded / anonymous structs.
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			bindNonBody(r, fv, verr)
			continue
		}

		bindField(r, field, fv, verr)
	}
}

// bindField fills a single struct field from whichever request sources its
// tags name (query / path / header / cookie).
func bindField(r *http.Request, field reflect.StructField, fv reflect.Value, verr *ValidationError) {
	if tag, ok := field.Tag.Lookup("query"); ok {
		key := parseTagName(tag)
		if err := bindValue(fv, field, r.URL.Query().Get(key)); err != nil {
			verr.add("query", key, field.Type, err)
		}
	}

	if tag, ok := field.Tag.Lookup("path"); ok {
		key := parseTagName(tag)
		val := ""
		if chiURLParam != nil {
			val = chiURLParam(r, key)
		}
		if err := bindValue(fv, field, val); err != nil {
			verr.add("path", key, field.Type, err)
		}
	}

	if tag, ok := field.Tag.Lookup("header"); ok {
		key := parseTagName(tag)
		if err := bindValue(fv, field, r.Header.Get(key)); err != nil {
			verr.add("header", key, field.Type, err)
		}
	}

	if tag, ok := field.Tag.Lookup("cookie"); ok {
		key := parseTagName(tag)
		val := ""
		if c, err := r.Cookie(key); err == nil {
			val = c.Value
		}
		if err := bindValue(fv, field, val); err != nil {
			verr.add("cookie", key, field.Type, err)
		}
	}
}

// bindValue sets fv from raw, falling back to the field's `default` tag when
// the request carries no value. A field with neither stays untouched.
func bindValue(fv reflect.Value, field reflect.StructField, raw string) error {
	if raw == "" {
		raw = field.Tag.Get("default")
	}
	if raw == "" {
		return nil
	}
	return setField(fv, raw)
}

// setField converts the string value to the appropriate Go type and sets it.
func setField(fv reflect.Value, raw string) error {
	// Dereference pointer, allocating if nil.
	if fv.Kind() == reflect.Ptr {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		fv = fv.Elem()
	}

	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return err
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return err
		}
		fv.SetFloat(n)
	default:
		return fmt.Errorf("unsupported kind %s", fv.Kind())
	}
	return nil
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
