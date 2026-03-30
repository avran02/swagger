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

// BindRequest populates dst from the incoming HTTP request using struct field tags:
//
//	json    – JSON request body (application/json)
//	query   – URL query parameters
//	path    – chi URL path parameters
//	header  – HTTP headers
//	cookie  – HTTP cookies
//
// dst must be a non-nil pointer to a struct.
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

	if err := bindBody(r, dst, rv); err != nil {
		return fmt.Errorf("binder: body: %w", err)
	}

	if err := bindNonBody(r, rv); err != nil {
		return err
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

// bindNonBody fills query / path / header / cookie fields via reflect.
func bindNonBody(r *http.Request, rv reflect.Value) error {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fv := rv.Field(i)

		if !fv.CanSet() {
			continue
		}

		// Recurse into embedded / anonymous structs.
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if err := bindNonBody(r, fv); err != nil {
				return err
			}
			continue
		}

		if tag, ok := field.Tag.Lookup("query"); ok {
			key, _ := parseTag(tag)
			if val := r.URL.Query().Get(key); val != "" {
				if err := setField(fv, val); err != nil {
					return fmt.Errorf("binder: query[%s]: %w", key, err)
				}
			}
		}

		if tag, ok := field.Tag.Lookup("path"); ok {
			key, _ := parseTag(tag)
			val := ""
			if chiURLParam != nil {
				val = chiURLParam(r, key)
			}
			if val != "" {
				if err := setField(fv, val); err != nil {
					return fmt.Errorf("binder: path[%s]: %w", key, err)
				}
			}
		}

		if tag, ok := field.Tag.Lookup("header"); ok {
			key, _ := parseTag(tag)
			if val := r.Header.Get(key); val != "" {
				if err := setField(fv, val); err != nil {
					return fmt.Errorf("binder: header[%s]: %w", key, err)
				}
			}
		}

		if tag, ok := field.Tag.Lookup("cookie"); ok {
			key, _ := parseTag(tag)
			if c, err := r.Cookie(key); err == nil {
				if err := setField(fv, c.Value); err != nil {
					return fmt.Errorf("binder: cookie[%s]: %w", key, err)
				}
			}
		}
	}
	return nil
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

// parseTag splits "name,option1,option2" and returns the name and raw options.
func parseTag(tag string) (string, string) {
	parts := strings.SplitN(tag, ",", 2)
	name := strings.TrimSpace(parts[0])
	opts := ""
	if len(parts) == 2 {
		opts = parts[1]
	}
	return name, opts
}
