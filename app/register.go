package app

import (
	"net/http"
	"strconv"
	"strings"

	"raices/env"
	"raices/helpers"
	"raices/templates/view/register"
)

const emanuelChurch = "INP Emanuel"

func offerFor(r *http.Request) register.Offer {
	path := r.URL.Path
	if path == "/emanuel" || strings.HasPrefix(path, "/emanuel/") {
		return register.Offer{
			Base:        "/emanuel",
			UnitCents:   env.TicketPriceMembersCents,
			FixedChurch: emanuelChurch,
		}
	}
	return register.Offer{
		Base:      "/register",
		UnitCents: env.TicketPriceCents,
	}
}

// ticketQtyMax matches the stepper cap in public/js/register.js.
const (
	ticketQtyMin = 1
	ticketQtyMax = 10
)

func handleRegisterQuantity(w http.ResponseWriter, r *http.Request) {
	offer := offerFor(r)
	qty, result := validateTicketQty(r.URL.Query().Get("qty"))
	if !result.Valid {
		qty = ticketQtyMin
	}

	if helpers.IsHTMX(r) {
		if err := helpers.RenderComponent(w, r, register.QuantityStep(offer, qty), nil); err != nil {
			logger.Error("render register quantity: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if err := helpers.RenderPage(w, r, register.QuantityPage(offer, qty)); err != nil {
		logger.Error("render register quantity: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func handleRegisterAmount(w http.ResponseWriter, r *http.Request) {
	offer := offerFor(r)
	if err := r.ParseForm(); err != nil {
		logger.Error("register amount parse form: %v", err)
		failRegisterAmount(w, r, offer, helpers.NewFormValidationResult(helpers.FormValidationError{
			Field:   "qty",
			Message: "request_unreadable",
		}))
		return
	}

	qty, result := validateTicketQty(r.FormValue("qty"))
	if !result.Valid {
		msg := helpers.GetFormErrorMessage(result, "qty")
		if msg == "" {
			msg = "ticket quantity is invalid"
		}
		logger.Error("register amount: %s", helpers.FormErrorText(r.Context(), msg))
		failRegisterAmount(w, r, offer, result)
		return
	}

	checkoutKey, err := newSecret()
	if err != nil {
		logger.Error("register amount checkout key: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if helpers.IsHTMX(r) {
		if err := helpers.RenderComponent(w, r, register.PayerStep(offer, qty, checkoutKey), nil); err != nil {
			logger.Error("render register amount: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	if err := helpers.RenderPage(w, r, register.Checkout(offer, qty, checkoutKey)); err != nil {
		logger.Error("render register amount: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func failRegisterAmount(w http.ResponseWriter, r *http.Request, offer register.Offer, result helpers.FormValidationResult) {
	if helpers.IsHTMX(r) {
		if err := helpers.RenderErrors(w, r, register.AmountErrors(result), helpers.HTMXFormError); err != nil {
			logger.Error("render register amount errors: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(helpers.HTMXFormError.Status)
	if err := helpers.RenderPage(w, r, register.Page(offer, result)); err != nil {
		logger.Error("render register amount: %v", err)
	}
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
