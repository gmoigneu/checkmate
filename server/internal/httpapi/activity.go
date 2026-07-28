package httpapi

import (
	"net/http"

	"github.com/nls/checkmate/server/internal/store"
)

func (s *Server) handleListTaskActivity(w http.ResponseWriter, r *http.Request) {
	ident, ok := s.caller(w, r)
	if !ok {
		return
	}

	p := newParams(r)
	_, limit, cursor := p.listOptions()
	if err := p.done(); err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	items, next, err := s.store.ListTaskActivity(r.Context(), ident.UserID, store.ActivityFilter{
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		s.writeStoreError(w, r, err)

		return
	}

	writeList(s, w, r, items, next)
}
