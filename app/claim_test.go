package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"raices/database"
	"raices/database/sqlc"
)

func TestClaimWithoutCookieDoesNotUseTicket(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, db := testMux(t, nil, now)
	token := "claim-token-plain"
	seedIssuedTickets(t, db, now, []string{token})

	req := httptest.NewRequest(http.MethodGet, "/tickets/claim/"+token, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?next=/tickets/claim/"+token {
		t.Fatalf("Location = %q", loc)
	}
	got, err := db.Queries.GetTicketByToken(context.Background(), sql.NullString{String: token, Valid: true})
	if err != nil {
		t.Fatalf("GetTicketByToken: %v", err)
	}
	if got.Status != "issued" || got.UsedAt.Valid {
		t.Fatalf("status=%s used_at=%v", got.Status, got.UsedAt)
	}
}

func TestClaimIssuedThenUsedKeepsUsedAt(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, db := testMux(t, nil, now)
	first, second := "token-first", "token-second"
	seedIssuedTickets(t, db, now, []string{first, second})
	held, err := db.Queries.CountTickets(context.Background())
	if err != nil {
		t.Fatalf("CountTickets: %v", err)
	}
	c := loginCookie(t, mux)

	rec := claimGET(t, mux, c, second)
	if rec.Code != http.StatusOK {
		t.Fatalf("first claim status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Boleto admitido") || !strings.Contains(body, "Ada Lovelace") || !strings.Contains(body, "Boleto 2") || !strings.Contains(body, "14 oct 2026, 12:00") {
		t.Fatalf("first claim body = %s", body)
	}
	ticket, err := db.Queries.GetTicketByToken(context.Background(), sql.NullString{String: second, Valid: true})
	if err != nil {
		t.Fatalf("GetTicketByToken: %v", err)
	}
	if ticket.Status != "used" || !ticket.UsedAt.Valid {
		t.Fatalf("status=%s used_at=%v", ticket.Status, ticket.UsedAt)
	}
	usedAt := ticket.UsedAt.String

	later := now.Add(5 * time.Minute)
	state.now = func() time.Time { return later }
	rec2 := claimGET(t, mux, c, second)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second claim status = %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "Este boleto ya fue usado") || !strings.Contains(rec2.Body.String(), "14 oct 2026, 12:00") {
		t.Fatalf("second claim body = %s", rec2.Body.String())
	}
	if strings.Contains(rec2.Body.String(), "12:05") {
		t.Fatal("second claim showed a new time")
	}
	again, err := db.Queries.GetTicketByToken(context.Background(), sql.NullString{String: second, Valid: true})
	if err != nil {
		t.Fatalf("GetTicketByToken after second: %v", err)
	}
	if again.UsedAt.String != usedAt {
		t.Fatalf("used_at changed from %q to %q", usedAt, again.UsedAt.String)
	}
	after, err := db.Queries.CountTickets(context.Background())
	if err != nil {
		t.Fatalf("CountTickets after: %v", err)
	}
	if after != held {
		t.Fatalf("held %d -> %d", held, after)
	}
}

func TestClaimUnknownToken(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, _ := testMux(t, nil, now)
	c := loginCookie(t, mux)
	rec := claimGET(t, mux, c, "missing-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "No encontramos ese boleto.") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func claimGET(t *testing.T, mux http.Handler, c *http.Cookie, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tickets/claim/"+token, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func seedIssuedTickets(t *testing.T, db *database.DB, now time.Time, tokens []string) {
	t.Helper()
	ctx := context.Background()
	order, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Ada",
		FamilyName:        "Lovelace",
		Email:             "ada@example.com",
		Age:               36,
		Quantity:          int64(len(tokens)),
		UnitPriceCents:    25000,
		TotalCents:        int64(25000 * len(tokens)),
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "sent",
		ReservedUntil:     now.UTC().Add(15 * time.Minute).Format(time.RFC3339),
		PollToken:         "poll-claim-" + t.Name(),
		CheckoutKey:       "ck-claim-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	for i, tok := range tokens {
		if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
			OrderID: order.ID,
			Token:   sql.NullString{String: tok, Valid: true},
			Status:  "issued",
		}); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
}
