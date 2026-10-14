package app

import (
	"net/http"
	"strings"

	"raices/helpers"
	"raices/templates/view/auth"
)

func handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		failLogin(w, r, "", helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "general",
			Message: "request_invalid",
		}))
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	result := validateLogin(email, r.FormValue("password"))
	if !result.Valid {
		failLogin(w, r, email, result)
		return
	}

	if helpers.IsHTMX(r) {
		w.Header().Set("HX-Redirect", "/register")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/register", http.StatusSeeOther)
}

func failLogin(w http.ResponseWriter, r *http.Request, email string, result helpers.FormValidationResult) {
	if helpers.IsHTMX(r) {
		if err := helpers.RenderErrors(w, r, auth.FormErrors(result), helpers.HTMXFormError); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(helpers.HTMXFormError.Status)
	_ = helpers.RenderPage(w, r, auth.Page(email, result))
}

func validateLogin(email, password string) helpers.FormValidationResult {
	var errs []helpers.FormValidationError
	switch {
	case email == "":
		errs = append(errs, helpers.FormValidationError{Field: "email", Message: "email_required"})
	case !looksLikeEmail(email):
		errs = append(errs, helpers.FormValidationError{Field: "email", Message: "email_invalid"})
	}
	switch {
	case password == "":
		errs = append(errs, helpers.FormValidationError{Field: "password", Message: "password_required"})
	case len(password) < 8:
		errs = append(errs, helpers.FormValidationError{Field: "password", Message: "password_short"})
	}
	return helpers.NewFormValidationResult(errs...)
}

func looksLikeEmail(value string) bool {
	if strings.ContainsAny(value, " \t") {
		return false
	}
	at := strings.Index(value, "@")
	if at <= 0 || at != strings.LastIndex(value, "@") || at == len(value)-1 {
		return false
	}
	domain := value[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1
}
