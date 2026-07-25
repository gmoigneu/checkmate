package httpapi

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/patch"
)

// maxTitleLength bounds a title so a runaway paste cannot fill a list view. The
// details field is where long text belongs.
const maxTitleLength = 500

// validDate reports whether s is a real YYYY-MM-DD calendar date.
//
// The schema's GLOB check only proves the shape, so 2026-02-31 would pass it;
// parsing here rejects dates that do not exist.
func validDate(s string) bool {
	_, err := time.Parse(database.DateOnly, s)

	return err == nil
}

// requireTitle validates a title on create.
func requireTitle(v *validationError, title string) string {
	trimmed := strings.TrimSpace(title)

	switch {
	case trimmed == "":
		v.add("title", "is required")
	case len(trimmed) > maxTitleLength:
		v.add("title", "must be at most 500 characters")
	}

	return trimmed
}

// checkTitlePatch validates a title on update, where absent is fine but null and
// empty are not.
func checkTitlePatch(v *validationError, f patch.Field[string]) patch.Field[string] {
	if !f.Set {
		return f
	}

	if f.Null {
		v.add("title", "cannot be null")

		return f
	}

	f.Value = strings.TrimSpace(f.Value)

	switch {
	case f.Value == "":
		v.add("title", "cannot be empty")
	case len(f.Value) > maxTitleLength:
		v.add("title", "must be at most 500 characters")
	}

	return f
}

// checkRequiredStringPatch validates a non-nullable, non-empty string field.
func checkRequiredStringPatch(v *validationError, field string, f patch.Field[string]) patch.Field[string] {
	if !f.Set {
		return f
	}

	if f.Null {
		v.add(field, "cannot be null")

		return f
	}

	f.Value = strings.TrimSpace(f.Value)
	if f.Value == "" {
		v.add(field, "cannot be empty")
	}

	return f
}

// checkEnum validates a value against a fixed set, ignoring the empty string so
// callers can leave it to a default.
func checkEnum(v *validationError, field, value string, allowed []string) {
	if value == "" {
		return
	}

	if !slices.Contains(allowed, value) {
		v.add(field, "must be one of "+strings.Join(allowed, ", "))
	}
}

// checkEnumPatch validates an enum on update.
func checkEnumPatch(v *validationError, field string, f patch.Field[string], allowed []string) {
	if !f.Set {
		return
	}

	if f.Null {
		v.add(field, "cannot be null")

		return
	}

	if !slices.Contains(allowed, f.Value) {
		v.add(field, "must be one of "+strings.Join(allowed, ", "))
	}
}

// checkDate validates an optional YYYY-MM-DD pointer.
func checkDate(v *validationError, field string, value *string) {
	if value == nil {
		return
	}

	if !validDate(*value) {
		v.add(field, "must be a YYYY-MM-DD date")
	}
}

// requireDate validates a mandatory YYYY-MM-DD value.
func requireDate(v *validationError, field, value string) {
	switch {
	case value == "":
		v.add(field, "is required")
	case !validDate(value):
		v.add(field, "must be a YYYY-MM-DD date")
	}
}

// checkDatePatch validates a YYYY-MM-DD field on update, where null clears it.
func checkDatePatch(v *validationError, field string, f patch.Field[string]) {
	if !f.Present() {
		return
	}

	if !validDate(f.Value) {
		v.add(field, "must be a YYYY-MM-DD date")
	}
}

// checkPositive validates an optional positive integer.
func checkPositive(v *validationError, field string, value *int64) {
	if value != nil && *value <= 0 {
		v.add(field, "must be greater than zero")
	}
}

// checkPositivePatch validates a positive integer on update, where null clears it.
func checkPositivePatch(v *validationError, field string, f patch.Field[int64]) {
	if f.Present() && f.Value <= 0 {
		v.add(field, "must be greater than zero")
	}
}

// checkNonNegativePatch validates a zero-or-greater integer on update.
func checkNonNegativePatch(v *validationError, field string, f patch.Field[int64]) {
	if f.Present() && f.Value < 0 {
		v.add(field, "cannot be negative")
	}
}

// checkTimezone validates an IANA timezone name.
func checkTimezone(v *validationError, field, value string) {
	if value == "" {
		return
	}

	if _, err := time.LoadLocation(value); err != nil {
		v.add(field, "is not a known IANA timezone")
	}
}

// checkTimezonePatch validates an IANA timezone on update.
func checkTimezonePatch(v *validationError, field string, f patch.Field[string]) {
	if !f.Set {
		return
	}

	if f.Null {
		v.add(field, "cannot be null")

		return
	}

	checkTimezone(v, field, f.Value)
}

// checkURL validates an optional absolute http(s) URL.
//
// References come from a Chrome extension and from Slack or email links, so an
// absolute URL with a host is the only useful shape.
func checkURL(v *validationError, field string, value *string) {
	if value == nil || *value == "" {
		return
	}

	if !validURL(*value) {
		v.add(field, "must be an absolute http or https URL")
	}
}

// checkURLPatch validates a URL on update, where null clears it.
func checkURLPatch(v *validationError, field string, f patch.Field[string]) {
	if !f.Present() || f.Value == "" {
		return
	}

	if !validURL(f.Value) {
		v.add(field, "must be an absolute http or https URL")
	}
}

func validURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	return parsed.Host != ""
}

// checkRRule validates the shape of an RFC 5545 recurrence rule.
//
// This is a structural check, not a full parser: it confirms a FREQ part with a
// known value and that every part is a KEY=VALUE pair, which catches typos and
// empty strings without pretending to implement all of RFC 5545.
func checkRRule(v *validationError, field, value string) {
	if strings.TrimSpace(value) == "" {
		v.add(field, "is required")

		return
	}

	freqValues := []string{"DAILY", "WEEKLY", "MONTHLY", "YEARLY", "HOURLY", "MINUTELY", "SECONDLY"}
	hasFreq := false

	for _, part := range strings.Split(strings.ToUpper(value), ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		key, val, found := strings.Cut(part, "=")
		if !found || strings.TrimSpace(key) == "" || strings.TrimSpace(val) == "" {
			v.add(field, "must be a semicolon-separated list of KEY=VALUE parts")

			return
		}

		if key == "FREQ" {
			hasFreq = true

			if !slices.Contains(freqValues, val) {
				v.add(field, "FREQ must be one of "+strings.Join(freqValues, ", "))

				return
			}
		}
	}

	if !hasFreq {
		v.add(field, "must include a FREQ part, e.g. FREQ=WEEKLY;BYDAY=MO")
	}
}

// checkRRulePatch validates an rrule on update.
func checkRRulePatch(v *validationError, field string, f patch.Field[string]) {
	if !f.Set {
		return
	}

	if f.Null {
		v.add(field, "cannot be null")

		return
	}

	checkRRule(v, field, f.Value)
}

// checkDateOrder rejects an end date that precedes the start date.
func checkDateOrder(v *validationError, startField, start, endField string, end *string) {
	if end == nil || start == "" || *end == "" {
		return
	}

	if !validDate(start) || !validDate(*end) {
		return
	}

	if *end < start {
		v.add(endField, "cannot be before "+startField)
	}
}
