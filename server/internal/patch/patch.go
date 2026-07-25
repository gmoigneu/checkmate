// Package patch models a JSON field that may be absent, explicitly null, or
// set to a value.
//
// PATCH needs all three states distinguished: `{}` must leave due_on alone,
// `{"due_on": null}` must clear it, and `{"due_on": "2026-07-25"}` must set it.
// A plain *string collapses the first two.
package patch

import "encoding/json"

// Field is an optionally-present, optionally-null JSON value.
type Field[T any] struct {
	// Set is true when the key was present in the request body at all.
	Set bool

	// Null is true when the key was present with a JSON null.
	Null bool

	// Value is meaningful only when Set is true and Null is false.
	Value T
}

// UnmarshalJSON records that the key was present. encoding/json only calls this
// for keys that actually appear in the object, which is what makes Set correct.
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.Set = true

	if string(b) == "null" {
		f.Null = true

		return nil
	}

	f.Null = false

	return json.Unmarshal(b, &f.Value)
}

// MarshalJSON renders an unset field as null; it exists so Field is symmetric
// and safe to embed in a struct that gets logged.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	if !f.Set || f.Null {
		return []byte("null"), nil
	}

	return json.Marshal(f.Value)
}

// Present reports whether the key was given with a non-null value.
func (f Field[T]) Present() bool { return f.Set && !f.Null }

// Ptr returns a pointer to the value, or nil when unset or null.
func (f Field[T]) Ptr() *T {
	if !f.Present() {
		return nil
	}

	v := f.Value

	return &v
}

// Or returns the value when present, otherwise fallback.
func (f Field[T]) Or(fallback T) T {
	if !f.Present() {
		return fallback
	}

	return f.Value
}
