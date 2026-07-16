package codec

import (
	"errors"
	"testing"
)

// resetToDefault restores the standard-library codec after a test that
// installs a custom one, since active is shared global state.
func resetToDefault(t *testing.T) {
	t.Cleanup(func() { active = stdCodec{} })
}

type point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

func TestMarshalUnmarshal_Default(t *testing.T) {
	t.Parallel()

	data, err := Marshal(point{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var p point
	if err := Unmarshal(data, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.X != 1 || p.Y != 2 {
		t.Errorf("got %+v, want {1 2}", p)
	}
}

func TestUnmarshalStrict_Default_RejectsUnknownField(t *testing.T) {
	t.Parallel()

	var p point
	err := UnmarshalStrict([]byte(`{"x":1,"y":2,"z":3}`), &p)
	if err == nil {
		t.Fatal("expected error for unknown field \"z\", got nil")
	}
}

func TestUnmarshalStrict_Default_AcceptsKnownFields(t *testing.T) {
	t.Parallel()

	var p point
	if err := UnmarshalStrict([]byte(`{"x":1,"y":2}`), &p); err != nil {
		t.Fatalf("UnmarshalStrict: %v", err)
	}
	if p.X != 1 || p.Y != 2 {
		t.Errorf("got %+v, want {1 2}", p)
	}
}

// permissiveCodec implements Codec but not StrictCodec.
type permissiveCodec struct{}

func (permissiveCodec) Marshal(v any) ([]byte, error) { return stdCodec{}.Marshal(v) }
func (permissiveCodec) Unmarshal(data []byte, v any) error {
	return stdCodec{}.Unmarshal(data, v)
}

func TestUnmarshalStrict_FallsBackWhenCodecIsNotStrict(t *testing.T) {
	resetToDefault(t)
	Set(permissiveCodec{})

	var p point
	// A codec without UnmarshalStrict must silently ignore unknown fields
	// rather than erroring — that's the documented fallback behavior.
	if err := UnmarshalStrict([]byte(`{"x":1,"y":2,"z":3}`), &p); err != nil {
		t.Fatalf("expected permissive fallback to succeed, got: %v", err)
	}
	if p.X != 1 || p.Y != 2 {
		t.Errorf("got %+v, want {1 2}", p)
	}
}

// customCodec implements StrictCodec so Set/UnmarshalStrict routes to it.
type customCodec struct {
	unmarshalStrictCalled bool
}

func (c *customCodec) Marshal(v any) ([]byte, error) { return stdCodec{}.Marshal(v) }
func (c *customCodec) Unmarshal(data []byte, v any) error {
	return stdCodec{}.Unmarshal(data, v)
}
func (c *customCodec) UnmarshalStrict(data []byte, v any) error {
	c.unmarshalStrictCalled = true
	return errors.New("boom")
}

func TestSet_CustomStrictCodec_IsUsed(t *testing.T) {
	resetToDefault(t)
	custom := &customCodec{}
	Set(custom)

	var p point
	err := UnmarshalStrict([]byte(`{"x":1,"y":2}`), &p)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected custom codec's error, got: %v", err)
	}
	if !custom.unmarshalStrictCalled {
		t.Error("expected UnmarshalStrict to be called on the custom codec")
	}
}
