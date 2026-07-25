package httpapi

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/nls/checkmate/server/internal/store"
)

// nullSentinel is the query value that means "match rows where this column is
// NULL": ?context_id=null selects the inbox.
const nullSentinel = "null"

// params reads and validates query-string filters, collecting problems as it
// goes so one response can report every bad parameter.
type params struct {
	query  url.Values
	errors *validationError
}

func newParams(r *http.Request) *params {
	return &params{query: r.URL.Query(), errors: newValidationError()}
}

// str returns a trimmed single value.
func (p *params) str(key string) string {
	return strings.TrimSpace(p.query.Get(key))
}

// csv returns a repeatable parameter, accepting both ?status=todo&status=done
// and ?status=todo,done, and rejecting values outside allowed.
func (p *params) csv(key string, allowed []string) []string {
	var out []string

	for _, raw := range p.query[key] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}

			if allowed != nil && !slices.Contains(allowed, part) {
				p.errors.add(key, "must be one of "+strings.Join(allowed, ", "))

				continue
			}

			if !slices.Contains(out, part) {
				out = append(out, part)
			}
		}
	}

	return out
}

// boolean reads a flag, defaulting to false when absent. A bare ?include_deleted
// with no value counts as true, which is how people type flags by hand.
func (p *params) boolean(key string) bool {
	raw, ok := p.query[key]
	if !ok || len(raw) == 0 {
		return false
	}

	value := strings.TrimSpace(raw[0])
	if value == "" {
		return true
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		p.errors.add(key, "must be true or false")

		return false
	}

	return parsed
}

// booleanPtr distinguishes an absent flag from one set to false.
func (p *params) booleanPtr(key string) *bool {
	if _, ok := p.query[key]; !ok {
		return nil
	}

	v := p.boolean(key)

	return &v
}

// limit reads the page size, leaving the store to clamp it.
func (p *params) limit() int {
	raw := p.str("limit")
	if raw == "" {
		return 0
	}

	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		p.errors.add("limit", "must be a positive integer")

		return 0
	}

	if v > store.MaxLimit {
		p.errors.add("limit", "must be at most "+strconv.Itoa(store.MaxLimit))

		return 0
	}

	return v
}

// date reads a YYYY-MM-DD parameter.
func (p *params) date(key string) string {
	raw := p.str(key)
	if raw == "" {
		return ""
	}

	if !validDate(raw) {
		p.errors.add(key, "must be a YYYY-MM-DD date")

		return ""
	}

	return raw
}

// nullableID reads an id parameter that also accepts the "null" sentinel. The
// second return value reports whether NULL was requested.
func (p *params) nullableID(key string) (string, bool) {
	raw := p.str(key)

	switch raw {
	case "":
		return "", false
	case nullSentinel:
		return "", true
	default:
		return raw, false
	}
}

// listOptions assembles the paging controls every list endpoint shares.
func (p *params) listOptions() (includeDeleted bool, limit int, cursor string) {
	return p.boolean("include_deleted"), p.limit(), p.str("cursor")
}

// done reports any accumulated problems, returning nil when the query was valid.
func (p *params) done() error {
	if p.errors.any() {
		return p.errors
	}

	return nil
}
