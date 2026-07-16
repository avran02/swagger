// Package codec lets callers swap the JSON implementation this library uses
// for request bodies and error responses (e.g. goccy/go-json, jsoniter,
// sonic) without the library taking a hard dependency on any of them.
package codec

import (
	"bytes"
	"encoding/json"
)

// Codec abstracts JSON encode/decode.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// StrictCodec is implemented by codecs that can reject unknown JSON object
// fields during Unmarshal — the equivalent of encoding/json's
// Decoder.DisallowUnknownFields. Codecs that don't implement it are used
// through UnmarshalStrict's permissive fallback: unknown body fields are
// silently ignored instead of producing a validation error.
type StrictCodec interface {
	Codec
	UnmarshalStrict(data []byte, v any) error
}

// stdCodec adapts the standard library's encoding/json.
type stdCodec struct{}

func (stdCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (stdCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func (stdCodec) UnmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// active is the codec used by Marshal / Unmarshal / UnmarshalStrict. Reads
// and writes are not synchronised — call Set only during startup, before
// handling requests, same as SetURLParamFunc / SetWalker.
var active Codec = stdCodec{}

// Set installs c as the JSON implementation used throughout the library.
// Call once during startup, e.g.:
//
//	codec.Set(gojsonCodec{}) // wraps goccy/go-json, jsoniter, sonic, ...
//
// c should implement StrictCodec if it can reject unknown fields; otherwise
// UnmarshalStrict silently falls back to permissive decoding.
func Set(c Codec) {
	active = c
}

// Marshal serialises v using the active codec.
func Marshal(v any) ([]byte, error) {
	return active.Marshal(v)
}

// Unmarshal decodes data into v using the active codec.
func Unmarshal(data []byte, v any) error {
	return active.Unmarshal(data, v)
}

// UnmarshalStrict decodes data into v, rejecting unknown object fields when
// the active codec supports it (StrictCodec). Codecs that don't implement
// StrictCodec fall back to plain Unmarshal — unknown fields are ignored
// rather than producing an error.
func UnmarshalStrict(data []byte, v any) error {
	if sc, ok := active.(StrictCodec); ok {
		return sc.UnmarshalStrict(data, v)
	}
	return active.Unmarshal(data, v)
}
