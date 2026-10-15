package app

import (
	"database/sql"
	"net/url"
	"strings"
	"time"

	"raices/database/sqlc"
	"raices/templates/view/register"
)

const (
	msgConfirming  = "El cargo sigue confirmándose."
	msgPaidSent    = "Pago confirmado. Los boletos se enviaron por correo."
	msgEmailFailed = "Recibimos el pago, pero el correo no salió. Contacta al organizador."
	msgUnfulfilled = "Recibimos el pago, pero ya no hay lugares. El organizador hará el reembolso."
	msgRejected    = "No se pudo cobrar. No se apartaron lugares."
	msgExpired     = "No se confirmó el pago. Si te cobraron, contacta al organizador."
	msgUnknownPay  = "No encontramos ese pago."
	msgSalesClosed = "Las compras cerraron."
)

func payPanel(checkoutKey, errMsg string) register.PanelView {
	return register.PanelView{
		Pay:         true,
		CheckoutKey: checkoutKey,
		Error:       errMsg,
	}
}

func orderPanel(order sqlc.Order, now time.Time, challengeURL string) register.PanelView {
	view := register.PanelView{Message: orderMessage(order, now)}
	switch order.Status {
	case "rejected":
		return view
	case "pending_payment":
		until, err := parseStamp(order.ReservedUntil)
		if err == nil && now.Before(until.Add(pollGrace)) {
			view.PollPath = pollPath(order.PollToken)
		}
		view.IFrameURL, view.LinkURL = challengeDisplay(challengeURL)
	case "paid":
		if order.FulfillmentStatus == "fulfilled" && order.EmailStatus == "sent" && order.PollToken != "" {
			view.Redirect = confirmedPath(order.PollToken)
			return view
		}
		if order.FulfillmentStatus == "fulfilled" && order.EmailStatus == "not_sent" && !emailStuck(order, now) {
			view.PollPath = pollPath(order.PollToken)
		}
	}
	return view
}

func pollPath(token string) string {
	return "/register/status?" + url.Values{"poll_token": {token}}.Encode()
}

func confirmedPath(token string) string {
	return "/register/confirmed?" + url.Values{"poll_token": {token}}.Encode()
}

func orderMessage(order sqlc.Order, now time.Time) string {
	switch order.Status {
	case "pending_payment":
		return msgConfirming
	case "rejected":
		return msgRejected
	case "expired":
		return msgExpired
	case "paid":
		if order.FulfillmentStatus == "unfulfilled" {
			return msgUnfulfilled
		}
		if order.EmailStatus == "sent" {
			return msgPaidSent
		}
		if order.EmailStatus == "failed" || emailStuck(order, now) {
			return msgEmailFailed
		}
		return msgConfirming
	default:
		return msgConfirming
	}
}

func attentionOrder(order sqlc.Order, now time.Time) bool {
	if order.Status == "expired" {
		return true
	}
	if order.Status == "paid" && order.FulfillmentStatus == "unfulfilled" {
		return true
	}
	if order.EmailStatus == "failed" {
		return true
	}
	return emailStuck(order, now)
}

func emailStuck(order sqlc.Order, now time.Time) bool {
	if order.Status != "paid" || order.FulfillmentStatus != "fulfilled" || order.EmailStatus != "not_sent" {
		return false
	}
	updated, err := parseStamp(order.UpdatedAt)
	if err != nil {
		return false
	}
	return now.Sub(updated) > emailStuckAfter
}

func parseStamp(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC)
}

func remainingSeats(held int64) int {
	n := ticketCapacity - int(held)
	if n < 0 {
		return 0
	}
	return n
}

func nullChurch(raw string) sql.NullString {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: raw, Valid: true}
}
