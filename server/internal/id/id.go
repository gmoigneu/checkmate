// Package id generates the identifiers used for every row in Checkmate.
package id

import (
	"fmt"

	"github.com/google/uuid"
)

// New returns a UUIDv7 as a lowercase string.
//
// v7 rather than v4 because the leading 48 bits are a millisecond timestamp:
// ids sort chronologically, which keeps sqlite's B-tree inserts at the right
// edge of the index, and clients can mint ids locally without coordination.
func New() string {
	v, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if crypto/rand fails, at which point the process
		// has no business continuing.
		panic(fmt.Sprintf("id: generate uuidv7: %v", err))
	}

	return v.String()
}

// Valid reports whether s parses as a UUID.
func Valid(s string) bool {
	_, err := uuid.Parse(s)

	return err == nil
}
