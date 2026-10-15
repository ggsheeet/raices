package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"raices/database/sqlc"
	"raices/env"
	"raices/helpers"
	"raices/templates/view/auth"
)

const sessionCookie = "session"
const sessionMaxAge = 7 * 24 * 60 * 60

func handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		failLogin(w, r, "", r.FormValue("next"), helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "general",
			Message: "request_invalid",
		}))
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	next := r.FormValue("next")
	if !credEqual(email, env.AdminEmail) || !credEqual(password, env.AdminPassword) {
		failLogin(w, r, email, next, helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "general",
			Message: "credentials_wrong",
		}))
		return
	}

	token, err := newSecret()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	expires := state.now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	if err := state.db.Queries.InsertSession(r.Context(), sqlc.InsertSessionParams{
		TokenHash: hashToken(token),
		ExpiresAt: expires,
	}); err != nil {
		logger.Error("insert session: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	dest := "/admin"
	if safeNext(next) {
		dest = next
	}
	if helpers.IsHTMX(r) {
		w.Header().Set("HX-Redirect", dest)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func failLogin(w http.ResponseWriter, r *http.Request, email, next string, result helpers.FormValidationResult) {
	if helpers.IsHTMX(r) {
		if err := helpers.RenderErrors(w, r, auth.FormErrors(result), helpers.HTMXFormError); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(helpers.HTMXFormError.Status)
	_ = helpers.RenderPage(w, r, auth.Page(email, next, result))
}

func credEqual(submitted, stored string) bool {
	if submitted == "" {
		return false
	}
	a := sha256.Sum256([]byte(submitted))
	b := sha256.Sum256([]byte(stored))
	return hmac.Equal(a[:], b[:])
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func safeNext(next string) bool {
	return strings.HasPrefix(next, "/") &&
		!strings.HasPrefix(next, "//") &&
		!strings.Contains(next, `\`) &&
		!strings.Contains(next, "@") &&
		!strings.Contains(next, "://")
}
