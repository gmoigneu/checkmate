package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/store"
)

// handleSync returns every change since a client's last cursor.
//
// Storage is sqlite with no offline mode, so there is no merge to perform: the
// server is authoritative and this is a one-way feed. A client persists the
// cursor, replays the rows onto its local copy, and applies tombstones as
// deletions.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	since := int64(0)

	if raw := p.str("since"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			p.errors.add("since", "must be a non-negative integer")
		} else {
			since = parsed
		}
	}

	limit := 0

	if raw := p.str("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)

		switch {
		case err != nil || parsed < 1:
			p.errors.add("limit", "must be a positive integer")
		case parsed > store.SyncMaxLimit:
			p.errors.add("limit", "must be at most "+strconv.Itoa(store.SyncMaxLimit))
		default:
			limit = parsed
		}
	}

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	result, err := s.store.Sync(r.Context(), ident.UserID, since, limit)
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, syncResponse{
		SyncResult: result,
		ServerTime: time.Now().UTC().Format(database.Timestamp),
	})
}

type syncResponse struct {
	store.SyncResult

	// ServerTime lets a client detect a badly skewed local clock, which otherwise
	// shows up as confusing due dates rather than as a clock problem.
	ServerTime string `json:"server_time"`
}

// handleBrief returns the daily brief.
func (s *Server) handleBrief(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)

	// The user's own timezone decides which day "today" is. Getting this from the
	// server's clock instead would hand someone in Paris the wrong day for the
	// first or last hours of it.
	timezone := ident.Timezone
	if requested := p.str("timezone"); requested != "" {
		timezone = requested
	}

	location, err := time.LoadLocation(timezone)
	if err != nil {
		p.errors.add("timezone", "is not a known IANA timezone")

		// Keep going so any other bad parameter is reported in the same response.
		location = time.UTC
	}

	date := p.date("date")
	if date == "" {
		date = time.Now().In(location).Format(database.DateOnly)
	}

	contextID := p.str("context_id")

	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	if _, err := s.store.ExpireRoutineTasksForUser(
		r.Context(), ident.UserID, time.Now().UTC(),
	); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	brief, err := s.store.Brief(r.Context(), ident.UserID, store.BriefFilter{
		Date:      date,
		Timezone:  location.String(),
		ContextID: contextID,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	s.writeJSON(w, r, http.StatusOK, brief)
}
