package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"raices/database/sqlc"
	"raices/helpers"
	"raices/payment"
	"raices/templates/view/register"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const (
	nameMaxRunes  = 100
	ageMin        = 1
	ageMax        = 120
	chargeTimeout = 20 * time.Second
)

type reserveErr struct {
	kind  string
	seats int
	order sqlc.Order
}

func (e *reserveErr) Error() string { return e.kind }

func handleRegisterPay(w http.ResponseWriter, r *http.Request) {
	if state.db == nil || state.charger == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	offer := offerFor(r)
	payPath := offer.Base + "/pay"
	if err := r.ParseForm(); err != nil {
		renderPayPanel(w, r, payPanel(payPath, "", helpers.FormErrorText(r.Context(), "request_unreadable")), helpers.HTMXFormError.Status)
		return
	}

	checkoutKey := strings.TrimSpace(r.FormValue("checkout_key"))
	now := state.now()
	ctx := r.Context()

	if checkoutKey != "" && state.db != nil {
		existing, err := state.db.Queries.GetOrderByCheckoutKey(ctx, checkoutKey)
		if err == nil {
			renderExistingOrder(w, r, existing, now, payPath)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	if strings.TrimSpace(r.FormValue("token")) == "" {
		renderPayPanel(w, r, payPanel(payPath, checkoutKey, helpers.FormErrorText(ctx, "token_required")), http.StatusBadRequest)
		return
	}

	payer, errMsg, ok := validatePayForm(r)
	if !ok {
		renderPayPanel(w, r, payPanel(payPath, checkoutKey, errMsg), http.StatusBadRequest)
		return
	}
	if checkoutKey == "" {
		renderPayPanel(w, r, payPanel(payPath, "", errMsg), http.StatusBadRequest)
		return
	}
	if offer.FixedChurch != "" {
		payer.church = offer.FixedChurch
	}

	order, err := reserveOrder(ctx, checkoutKey, payer, now, offer.UnitCents)
	if err != nil {
		var re *reserveErr
		if errors.As(err, &re) {
			switch re.kind {
			case "sales_closed":
				renderPayPanel(w, r, payPanel(payPath, checkoutKey, msgSalesClosed), http.StatusBadRequest)
				return
			case "capacity":
				renderPayPanel(w, r, payPanel(payPath, checkoutKey, fmt.Sprintf("Quedan %d lugares.", re.seats)), http.StatusBadRequest)
				return
			case "duplicate":
				renderExistingOrder(w, r, re.order, now, payPath)
				return
			}
		}
		logger.Error("reserve: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	chargeCtx, cancel := context.WithTimeout(ctx, chargeTimeout)
	defer cancel()
	id := strconv.FormatInt(order.ID, 10)
	result := state.charger.Create(chargeCtx, payment.Charge{
		LocalOrderID:    id,
		IdempotencyKey:  checkoutKey,
		Token:           strings.TrimSpace(r.FormValue("token")),
		PaymentMethodID: strings.TrimSpace(r.FormValue("payment_method_id")),
		TotalCents:      int(order.TotalCents),
		GivenName:       payer.givenName,
		FamilyName:      payer.familyName,
		Email:           payer.email,
	})

	if chargeFailed(result) {
		if err := rejectReserved(ctx, order.ID, now); err != nil {
			logger.Error("reject reserved: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		fresh, err := newSecret()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		renderPayPanel(w, r, payPanel(payPath, fresh, msgRejected), http.StatusBadRequest)
		return
	}

	if result.MPOrderID != "" {
		if err := state.db.Queries.UpdateOrderMpID(ctx, sqlc.UpdateOrderMpIDParams{
			MpOrderID: sql.NullString{String: result.MPOrderID, Valid: true},
			ID:        order.ID,
		}); err != nil {
			logger.Error("mp_order_id: %v", err)
		}
	}

	challenge := ""
	if isChallenge(result.Status, result.StatusDetail) {
		challenge = result.ChallengeURL
	}
	renderPayPanel(w, r, orderPanel(order, now, challenge), http.StatusOK)
}

func renderExistingOrder(w http.ResponseWriter, r *http.Request, order sqlc.Order, now time.Time, payPath string) {
	if order.Status == "rejected" {
		fresh, err := newSecret()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		renderPayPanel(w, r, payPanel(payPath, fresh, msgRejected), http.StatusBadRequest)
		return
	}
	renderPayPanel(w, r, orderPanel(order, now, ""), http.StatusOK)
}

func renderPayPanel(w http.ResponseWriter, r *http.Request, view register.PanelView, status int) {
	if view.Redirect != "" {
		if helpers.IsHTMX(r) {
			w.Header().Set("HX-Redirect", view.Redirect)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, view.Redirect, http.StatusSeeOther)
		return
	}
	if helpers.IsHTMX(r) {
		var custom error
		if status != http.StatusOK {
			custom = helpers.HTMXError{Code: "ERR_FORM_ERROR", Message: "Errors handled by form", Status: status}
		}
		if err := helpers.RenderComponent(w, r, register.CheckoutPanel(view), custom); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = register.CheckoutPanel(view).Render(r.Context(), w)
}

type payerInput struct {
	givenName, familyName, email, church string
	age, qty                             int
}

func validatePayForm(r *http.Request) (payerInput, string, bool) {
	qty, result := validateTicketQty(r.FormValue("qty"))
	if !result.Valid {
		return payerInput{}, helpers.FormErrorText(r.Context(), helpers.GetFormErrorMessage(result, "qty")), false
	}
	given := strings.TrimSpace(r.FormValue("given_name"))
	family := strings.TrimSpace(r.FormValue("family_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	confirm := strings.TrimSpace(r.FormValue("email_confirm"))
	ageRaw := strings.TrimSpace(r.FormValue("age"))
	church := r.FormValue("church")

	if given == "" {
		return payerInput{}, helpers.FormErrorText(r.Context(), "name_required"), false
	}
	if utf8.RuneCountInString(given) > nameMaxRunes {
		return payerInput{}, helpers.FormErrorText(r.Context(), "name_too_long"), false
	}
	if family == "" {
		return payerInput{}, helpers.FormErrorText(r.Context(), "name_required"), false
	}
	if utf8.RuneCountInString(family) > nameMaxRunes {
		return payerInput{}, helpers.FormErrorText(r.Context(), "name_too_long"), false
	}
	if email == "" {
		return payerInput{}, helpers.FormErrorText(r.Context(), "email_required"), false
	}
	if !strings.EqualFold(email, confirm) {
		return payerInput{}, helpers.FormErrorText(r.Context(), "email_mismatch"), false
	}
	age, err := strconv.Atoi(ageRaw)
	if err != nil {
		return payerInput{}, helpers.FormErrorText(r.Context(), "age_invalid"), false
	}
	if age < ageMin || age > ageMax {
		return payerInput{}, helpers.FormErrorText(r.Context(), "age_range"), false
	}

	return payerInput{
		givenName:  given,
		familyName: family,
		email:      email,
		church:     church,
		age:        age,
		qty:        qty,
	}, "", true
}

func reserveOrder(ctx context.Context, checkoutKey string, payer payerInput, now time.Time, unitCents int) (sqlc.Order, error) {
	var order sqlc.Order
	err := state.db.InTx(ctx, func(q *sqlc.Queries) error {
		if err := ExpireStale(ctx, q, now); err != nil {
			return err
		}
		if !SalesOpen(now) {
			return &reserveErr{kind: "sales_closed"}
		}
		held, err := q.CountTickets(ctx)
		if err != nil {
			return err
		}
		left := remainingSeats(held)
		if left < payer.qty {
			return &reserveErr{kind: "capacity", seats: left}
		}
		pollToken, err := newSecret()
		if err != nil {
			return err
		}
		unit := unitCents
		row, err := q.InsertOrder(ctx, sqlc.InsertOrderParams{
			GivenName:         payer.givenName,
			FamilyName:        payer.familyName,
			Email:             payer.email,
			Age:               int64(payer.age),
			Church:            nullChurch(payer.church),
			Quantity:          int64(payer.qty),
			UnitPriceCents:    int64(unit),
			TotalCents:        int64(unit * payer.qty),
			Status:            "pending_payment",
			FulfillmentStatus: "pending",
			EmailStatus:       "not_sent",
			ReservedUntil:     reservedUntilStamp(now),
			PollToken:         pollToken,
			CheckoutKey:       checkoutKey,
		})
		if err != nil {
			if isConstraint(err) {
				winner, getErr := q.GetOrderByCheckoutKey(ctx, checkoutKey)
				if getErr == nil {
					return &reserveErr{kind: "duplicate", order: winner}
				}
			}
			return err
		}
		for i := 0; i < payer.qty; i++ {
			if _, err := q.InsertTicket(ctx, sqlc.InsertTicketParams{
				OrderID: row.ID,
				Token:   sql.NullString{},
				Status:  "reserved",
			}); err != nil {
				return err
			}
		}
		order = row
		return nil
	})
	return order, err
}

func rejectReserved(ctx context.Context, orderID int64, now time.Time) error {
	return state.db.InTx(ctx, func(q *sqlc.Queries) error {
		if err := q.DeleteReservedTicketsByOrder(ctx, orderID); err != nil {
			return err
		}
		return q.RejectPendingOrder(ctx, sqlc.RejectPendingOrderParams{
			UpdatedAt: sqliteNow(now),
			ID:        orderID,
		})
	})
}

func isConstraint(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}

func handleRegisterStatus(w http.ResponseWriter, r *http.Request) {
	now := state.now()
	ctx := r.Context()
	if state.db == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if err := state.db.InTx(ctx, func(q *sqlc.Queries) error {
		return ExpireStale(ctx, q, now)
	}); err != nil {
		logger.Error("expire stale: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("poll_token"))
	if token == "" {
		renderPayPanel(w, r, register.PanelView{Message: msgUnknownPay}, http.StatusNotFound)
		return
	}
	order, err := state.db.Queries.GetOrderByPollToken(ctx, token)
	if err != nil {
		renderPayPanel(w, r, register.PanelView{Message: msgUnknownPay}, http.StatusNotFound)
		return
	}
	renderPayPanel(w, r, orderPanel(order, now, ""), http.StatusOK)
}
