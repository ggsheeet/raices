package app

import (
	"net/http"
	"strconv"
	"strings"

	"raices/helpers"
	"raices/templates/view/register"
)

// ticketQtyMax matches the stepper cap in public/js/register.js.
const (
	ticketQtyMin = 1
	ticketQtyMax = 10
)

func handleRegisterQuantity(w http.ResponseWriter, r *http.Request) {
	qty, result := validateTicketQty(r.URL.Query().Get("qty"))
	if !result.Valid {
		qty = ticketQtyMin
	}

	if helpers.IsHTMX(r) {
		if err := helpers.RenderComponent(w, r, register.QuantityStep(qty), nil); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if err := helpers.RenderPage(w, r, register.QuantityPage(qty)); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func handleRegisterAmount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		failRegisterAmount(w, r, helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "qty",
			Message: "request_unreadable",
		}))
		return
	}

	qty, result := validateTicketQty(r.FormValue("qty"))
	if !result.Valid {
		failRegisterAmount(w, r, result)
		return
	}

	if helpers.IsHTMX(r) {
		if err := helpers.RenderComponent(w, r, register.PayerStep(qty), nil); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if err := helpers.RenderPage(w, r, register.Checkout(qty)); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func failRegisterAmount(w http.ResponseWriter, r *http.Request, result helpers.FormValidationResult) {
	if helpers.IsHTMX(r) {
		if err := helpers.RenderErrors(w, r, register.AmountErrors(result), helpers.HTMXFormError); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(helpers.HTMXFormError.Status)
	_ = helpers.RenderPage(w, r, register.Page(result))
}

func validateTicketQty(raw string) (int, helpers.FormValidationResult) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "qty",
			Message: "qty_required",
		})
	}

	qty, err := strconv.Atoi(raw)
	if err != nil || qty < ticketQtyMin || qty > ticketQtyMax {
		message := "qty_invalid"
		if err == nil {
			message = "qty_range"
		}
		return 0, helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "qty",
			Message: message,
		})
	}

	return qty, helpers.NewFormValidationResult()
}
