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
	ok, err := requireSession(w, r)
	if err != nil {
		logger.Error("claim session: %v", err)
		return
	}
	if !ok {
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
			logger.Error("claim: ticket not found")
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
		logger.Error("claim: ticket is not claimable")
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
	index, err := ticketIndex(r, ticket)
	if err != nil {
		logger.Error("claim ticket index: %v", err)
	}
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

func ticketIndex(r *http.Request, ticket sqlc.Ticket) (int, error) {
	tickets, err := state.db.Queries.ListTicketsByOrder(r.Context(), ticket.OrderID)
	if err != nil {
		return 0, err
	}
	for i, row := range tickets {
		if row.ID == ticket.ID {
			return i + 1, nil
		}
	}
	return 0, nil
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
