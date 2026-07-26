package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Task sorting, and the pagination that has to survive it.
//
// The default order is priority first (urgent, high, medium, low, unprioritized)
// and newest-created-first within each rank. ids are UUIDv7, so id DESC supplies
// the creation-time tiebreak without another stored value.
//
// Any other order needs a keyset cursor rather than an offset. Offsets drift when
// rows are inserted or updated between pages, which for a task list means quietly
// skipping or repeating a task. A keyset carries the last row's sort value and id,
// and asks for "everything after this exact position".
//
// Two details make that awkward, and both are handled here rather than left to the
// caller:
//
//   - NULLs. An undated task has no due_on, and "sorted by due date" almost always
//     means undated last, whichever direction the dates run. So the sort key is a
//     coalesce with a sentinel picked per direction, which also makes the key
//     totally ordered and therefore safe to compare in a keyset.
//   - Ties. Two tasks can share a due date, so the key alone is not a position.
//     Every sort is (key, id), and id is unique, which makes the position exact.

// SortField names a sortable column.
type SortField string

// Sortable fields. Omitting the field uses priority followed by creation time.
const (
	SortPriority        SortField = "priority"
	SortCreatedAt       SortField = "created_at"
	SortUpdatedAt       SortField = "updated_at"
	SortDueOn           SortField = "due_on"
	SortPlannedOn       SortField = "planned_on"
	SortTitle           SortField = "title"
	SortEstimateMinutes SortField = "estimate_minutes"
	SortCompletedAt     SortField = "completed_at"
	SortStatus          SortField = "status"
)

// SortFields is every accepted value, for validation and for documenting the API.
var SortFields = []string{
	string(SortPriority), string(SortCreatedAt), string(SortUpdatedAt), string(SortDueOn),
	string(SortPlannedOn), string(SortTitle), string(SortEstimateMinutes),
	string(SortCompletedAt), string(SortStatus),
}

// SortOrders is the accepted directions.
var SortOrders = []string{"asc", "desc"}

// ValidSortField reports whether a field can be sorted on.
func ValidSortField(field string) bool { return slices.Contains(SortFields, field) }

// ValidSortOrder reports whether a direction is understood.
func ValidSortOrder(order string) bool { return slices.Contains(SortOrders, order) }

// sortPlan is a resolved sort: the SQL key expression, the direction, and how to
// read a cursor value back.
type sortPlan struct {
	// signature identifies this ordering, and is recorded in any cursor it issues
	// so a cursor cannot be replayed under a different sort.
	signature string

	// keyExpr is the SQL expression to order and compare by. Built from package
	// literals only; no caller input reaches it.
	keyExpr string

	// dir is "ASC" or "DESC".
	dir string

	// idDir is the direction for the unique creation-time tiebreak. It normally
	// matches dir; priority always uses newest first within a rank.
	idDir string

	// numeric is true when the key is an integer, so a cursor value has to be
	// bound as one rather than as text.
	numeric bool

	// byIDOnly is true for a created_at sort, where the id is the key and no
	// separate expression is needed.
	byIDOnly bool
}

// resolveSort turns a requested field and direction into a plan.
//
// The sentinel in each coalesce is chosen so that missing values land at the end
// whichever way the sort runs: a high sentinel when ascending, a low one when
// descending. "Sorted by due date" showing undated tasks first would be useless in
// both directions.
func resolveSort(field, order string) sortPlan {
	if field == "" {
		if order == "" {
			return sortPlan{
				signature: "priority:asc,created_at:desc",
				keyExpr:   priorityKeyExpr(true),
				dir:       "ASC",
				idDir:     "DESC",
				numeric:   true,
			}
		}

		// Before priority became the composite default, an order without a sort
		// controlled created_at. Keep honoring that accepted request shape rather
		// than silently discarding a client's direction.
		field = string(SortCreatedAt)
	}

	if order == "" {
		// Newest first for creation time, which is what a task list wants;
		// earliest first for everything else, since "sorted by due date" means the
		// soonest deadline at the top.
		if field == string(SortCreatedAt) {
			order = "desc"
		} else {
			order = "asc"
		}
	}

	asc := order == "asc"

	dir := "DESC"
	if asc {
		dir = "ASC"
	}

	plan := sortPlan{dir: dir, idDir: dir, signature: field + ":" + order}

	switch SortField(field) {
	case SortPriority:
		plan.keyExpr = priorityKeyExpr(asc)
		plan.idDir = "DESC"
		plan.numeric = true

	case SortCreatedAt:
		// id is a UUIDv7, so it already orders by creation time.
		plan.byIDOnly = true

	case SortUpdatedAt:
		plan.keyExpr = "updated_at"

	case SortTitle:
		// NOCASE so "apple" and "Apple" sort together rather than in ASCII order.
		plan.keyExpr = "title COLLATE NOCASE"

	case SortStatus:
		plan.keyExpr = "status"

	case SortDueOn:
		plan.keyExpr = dateKeyExpr("due_on", asc)

	case SortPlannedOn:
		plan.keyExpr = dateKeyExpr("planned_on", asc)

	case SortCompletedAt:
		plan.keyExpr = dateKeyExpr("completed_at", asc)

	case SortEstimateMinutes:
		plan.numeric = true

		if asc {
			plan.keyExpr = "coalesce(estimate_minutes, 9223372036854775807)"
		} else {
			plan.keyExpr = "coalesce(estimate_minutes, -1)"
		}

	default:
		plan.byIDOnly = true
	}

	return plan
}

// priorityRankExpr maps every non-null priority to its integer rank. The
// migration's matching index repeats this immutable SQL expression because a
// migration cannot import Go constants.
const priorityRankExpr = `CASE priority
	WHEN 'urgent' THEN 0
	WHEN 'high' THEN 1
	WHEN 'medium' THEN 2
	WHEN 'low' THEN 3
	END`

// priorityKeyExpr adds a direction-specific null sentinel so an unprioritized
// task stays last in either direction.
func priorityKeyExpr(asc bool) string {
	if asc {
		return "coalesce(" + priorityRankExpr + ", 4)"
	}

	return "coalesce(" + priorityRankExpr + ", -1)"
}

// dateKeyExpr coalesces a nullable date or timestamp so missing values sort last.
func dateKeyExpr(column string, asc bool) string {
	if asc {
		// Sorts after any real date.
		return `coalesce(` + column + `, '9999-12-31')`
	}

	// Sorts before any real date, so it lands last in a descending sort.
	return `coalesce(` + column + `, '')`
}

// orderBy renders the ORDER BY clause. The id tiebreak is what makes a page
// boundary a single unambiguous position.
func (p sortPlan) orderBy() string {
	if p.byIDOnly {
		return " ORDER BY id " + p.idDir
	}

	return " ORDER BY " + p.keyExpr + " " + p.dir + ", id " + p.idDir
}

// keysetCursor is the position of the last row on a page.
type keysetCursor struct {
	// Key is the sort key value as text; absent for the default sort.
	Key *string `json:"k,omitempty"`

	// ID is always present and breaks ties.
	ID string `json:"i"`

	// Sort names the sort this cursor was issued for, as "field:direction".
	//
	// A position is only meaningful under the ordering that produced it: carrying
	// a due-date cursor into a title sort would compare a date against a title and
	// return a page that is wrong rather than merely surprising. Recording it lets
	// the mismatch be refused instead of silently answered.
	Sort string `json:"s,omitempty"`
}

// encodeCursor renders a cursor as an opaque token.
//
// Opaque on purpose: it is base64 of JSON, and clients are documented not to parse
// it, so the encoding can change when a new sort arrives without breaking them.
func encodeCursor(c keysetCursor) string {
	encoded, err := json.Marshal(c)
	if err != nil {
		// keysetCursor is two strings; marshalling cannot fail. Returning the bare
		// id keeps paging working rather than losing the page entirely.
		return c.ID
	}

	return base64.RawURLEncoding.EncodeToString(encoded)
}

// decodeCursor parses a cursor token.
//
// A plain id is accepted as well as the encoded form, because that is what earlier
// cursors were, and a client that persisted one should not be broken by an upgrade.
func decodeCursor(token string) (keysetCursor, error) {
	if token == "" {
		return keysetCursor{}, nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return keysetCursor{ID: token}, nil
	}

	var c keysetCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		// Decoded as base64 but is not our JSON, so treat it as an opaque id.
		return keysetCursor{ID: token}, nil
	}

	if c.ID == "" {
		return keysetCursor{}, fmt.Errorf("store: cursor has no id")
	}

	return c, nil
}

// applyKeyset adds the "everything after this position" condition.
//
// The comparison is lexicographic over (key, id): strictly past the key, or equal
// on the key and strictly past the id. Written out rather than using sqlite's row
// values so the bound types stay explicit.
func (p sortPlan) applyKeyset(c *conditions, cursor keysetCursor) error {
	if cursor.ID == "" {
		return nil
	}

	// A cursor carrying a signature must match this sort. An empty signature is a
	// bare id from before cursors were sort-aware, which is only usable on the
	// default ordering.
	switch {
	case cursor.Sort != "" && cursor.Sort != p.signature:
		return fmt.Errorf("store: cursor was issued for sort %q, not %q",
			cursor.Sort, p.signature)
	case cursor.Sort == "" && !p.byIDOnly:
		return fmt.Errorf("store: cursor carries no sort and cannot be used with %q",
			p.signature)
	}

	if p.byIDOnly {
		if p.idDir == "ASC" {
			c.add("id > ?", cursor.ID)
		} else {
			c.add("id < ?", cursor.ID)
		}

		return nil
	}

	if cursor.Key == nil {
		return fmt.Errorf("store: cursor is missing the sort key; it was issued for a different sort")
	}

	var key any = *cursor.Key

	if p.numeric {
		parsed, err := strconv.ParseInt(*cursor.Key, 10, 64)
		if err != nil {
			return fmt.Errorf("store: cursor key %q is not a number", *cursor.Key)
		}

		key = parsed
	}

	keyComparison := "<"
	if p.dir == "ASC" {
		keyComparison = ">"
	}

	idComparison := "<"
	if p.idDir == "ASC" {
		idComparison = ">"
	}

	c.add(
		"("+p.keyExpr+" "+keyComparison+" ? OR ("+p.keyExpr+" = ? AND id "+idComparison+" ?))",
		key, key, cursor.ID,
	)

	return nil
}

// selectColumns adds the sort key to the projection so the next cursor can be
// built from the row that was actually returned, rather than recomputed in Go from
// a value that might not match sqlite's collation.
func (p sortPlan) selectColumns(base string) string {
	if p.byIDOnly {
		return base
	}

	return base + ", " + p.keyExpr + " AS __sort_key"
}

// nextCursor builds the cursor for the last row of a page.
func (p sortPlan) nextCursor(id string, key *string) string {
	c := keysetCursor{ID: id, Sort: p.signature}
	if !p.byIDOnly {
		c.Key = key
	}

	return encodeCursor(c)
}

// describeSort renders the effective sort for logging and for the API to echo.
func (p sortPlan) describeSort(field string) string {
	if field == "" {
		return "priority asc, created_at desc"
	}

	return field + " " + strings.ToLower(p.dir)
}
