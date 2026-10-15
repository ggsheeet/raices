package app

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"raices/database/sqlc"
	"raices/helpers"
	"raices/payment"
	"raices/templates/view/register"
)

func handleRegisterConfirmed(w http.ResponseWriter, r *http.Request) {
	if state.db == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("poll_token"))
	if token == "" {
		renderReceipt(w, r, missingReceipt())
		return
	}
	order, err := state.db.Queries.GetOrderByPollToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			renderReceipt(w, r, missingReceipt())
			return
		}
		logger.Error("confirmed order: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	renderReceipt(w, r, receiptFor(order))
}

func renderReceipt(w http.ResponseWriter, r *http.Request, view register.Receipt) {
	if err := helpers.RenderPage(w, r, register.Confirmed(view)); err != nil {
		logger.Error("render confirmed: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func missingReceipt() register.Receipt {
	return register.Receipt{
		Title:  "No encontramos ese pago",
		Detail: "El enlace no corresponde a un registro. Puedes volver a empezar.",
	}
}

func receiptFor(order sqlc.Order) register.Receipt {
	if order.Status != "paid" || order.FulfillmentStatus != "fulfilled" || order.EmailStatus != "sent" {
		return register.Receipt{
			Title:  "Registro",
			Lead:   orderMessage(order, state.now()),
			Detail: "Puedes volver al registro.",
		}
	}
	qty := int(order.Quantity)
	lead := "Tus boletos ya están listos"
	detail := fmt.Sprintf("Enviamos tus %d boletos a %s. Revisa tu bandeja de entrada y, si no aparecen, la carpeta de spam.", qty, order.Email)
	if qty == 1 {
		lead = "Tu boleto ya está listo"
		detail = fmt.Sprintf("Enviamos tu boleto a %s. Revisa tu bandeja de entrada y, si no aparece, la carpeta de spam.", order.Email)
	}
	return register.Receipt{
		OK:     true,
		Title:  "Pago confirmado",
		Lead:   lead,
		Detail: detail,
		Facts: []register.Fact{
			{Label: "A nombre de", Value: strings.TrimSpace(order.GivenName + " " + order.FamilyName)},
			{Label: "Correo", Value: order.Email},
			{Label: "Boletos", Value: fmt.Sprintf("%d", qty)},
			{Label: "Total", Value: "Mex$" + payment.FormatCents(int(order.TotalCents))},
			{Label: "Fechas", Value: "30 oct – 1 nov 2026"},
			{Label: "Lugar", Value: "INP Emanuel Dios con Nosotros"},
		},
	}
}
