package app

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func validSession(r *http.Request) (bool, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return false, nil
	}
	sess, err := state.db.Queries.GetSession(r.Context(), hashToken(c.Value))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil {
		return false, fmt.Errorf("parse session expiry: %w", err)
	}
	return exp.After(state.now().UTC()), nil
}

func requireSession(w http.ResponseWriter, r *http.Request) (bool, error) {
	ok, err := validSession(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return false, err
	}
	if ok {
		return true, nil
	}
	http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
	return false, nil
}
