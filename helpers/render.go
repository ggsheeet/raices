package helpers

import (
	"errors"
	"net/http"

	"github.com/a-h/templ"
)

type HTMXError struct {
	Code    string
	Message string
	Status  int
}

func (e HTMXError) Error() string {
	return e.Message
}

var HTMXFormError = HTMXError{
	Code:    "ERR_FORM_ERROR",
	Message: "Errors handled by form",
	Status:  http.StatusBadRequest,
}

func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func RenderPage(w http.ResponseWriter, r *http.Request, page templ.Component) error {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	return page.Render(r.Context(), w)
}

func RenderErrors(w http.ResponseWriter, r *http.Request, component templ.Component, customErr error) error {
	w.Header().Set("HX-Reswap", "none")
	return RenderComponent(w, r, component, customErr)
}

func RenderComponent(w http.ResponseWriter, r *http.Request, component templ.Component, customErr error) error {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	var formErr HTMXError
	if errors.As(customErr, &formErr) && formErr.Status != 0 {
		w.WriteHeader(formErr.Status)
	}
	return component.Render(r.Context(), w)
}
