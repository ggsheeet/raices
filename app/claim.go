package app

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"raices/database/sqlc"
	"raices/helpers"
	"raices/templates/view/claim"
)

var mexicoCity = time.FixedZone("CST", -6*60*60)

func handleClaim(w http.ResponseWriter, r *http.Request) {
	if !requireSession(w, r) {
		return
	}

	token := r.PathValue("token")
	now := state.now().UTC()
	stamp := now.Format(time.RFC3339)
	n, err := state.db.Queries.ClaimIssuedTicket(r.Context(), sqlc.ClaimIssuedTicketParams{
		UsedAt:    sql.NullString{String: stamp, Valid: true},
		UpdatedAt: stamp,
		Token:     sql.NullString{String: token, Valid: token != ""},
	})
	if err != nil {
		logger.Error("claim ticket: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ticket, err := state.db.Queries.GetTicketByToken(r.Context(), sql.NullString{String: token, Valid: token != ""})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if err := helpers.RenderPage(w, r, claim.NotFound()); err != nil {
				logger.Error("render claim missing: %v", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
			return
		}
		logger.Error("get ticket: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if n == 0 && ticket.Status != "used" {
		if err := helpers.RenderPage(w, r, claim.NotFound()); err != nil {
			logger.Error("render claim missing: %v", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}

	order, err := state.db.Queries.GetOrderByID(r.Context(), ticket.OrderID)
	if err != nil {
		logger.Error("get order for claim: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	index := ticketIndex(r, ticket)
	usedAt := ticket.UsedAt.String
	if n == 1 {
		usedAt = stamp
	}
	if err := helpers.RenderPage(w, r, claim.Ticket(claim.TicketView{
		Name:   strings.TrimSpace(order.GivenName + " " + order.FamilyName),
		Index:  index,
		UsedAt: formatMexico(usedAt),
		Fresh:  n == 1,
	})); err != nil {
		logger.Error("render claim: %v", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func ticketIndex(r *http.Request, ticket sqlc.Ticket) int {
	tickets, err := state.db.Queries.ListTicketsByOrder(r.Context(), ticket.OrderID)
	if err != nil {
		return 0
	}
	for i, row := range tickets {
		if row.ID == ticket.ID {
			return i + 1
		}
	}
	return 0
}

func formatMexico(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	t = t.In(mexicoCity)
	months := [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), months[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}
