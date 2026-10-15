package app

import (
	"net/http"
	"time"
)

func validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false
	}
	sess, err := state.db.Queries.GetSession(r.Context(), hashToken(c.Value))
	if err != nil {
		return false
	}
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil {
		return false
	}
	return exp.After(state.now().UTC())
}

func requireSession(w http.ResponseWriter, r *http.Request) bool {
	if validSession(r) {
		return true
	}
	http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
	return false
}
