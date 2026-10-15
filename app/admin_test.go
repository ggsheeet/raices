package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"raices/database"
	"raices/database/sqlc"
)

type adminSeeds struct {
	unfulfilled sqlc.Order
	failed      sqlc.Order
	stuck       sqlc.Order
	expired     sqlc.Order
	sent        sqlc.Order
	pending     sqlc.Order
	rejected    sqlc.Order
	fresh       sqlc.Order
}

func TestAdminAttentionFilter(t *testing.T) {
	now := openNow()
	mux, db, _ := testMuxSender(t, nil, nil, now)
	seeds := seedAdminOrders(t, db, now)
	body := getAdmin(t, mux, "/admin")

	assertContains(t, body, seeds.unfulfilled.Email, seeds.failed.Email, seeds.stuck.Email, seeds.expired.Email)
	assertNotContains(t, body, seeds.sent.Email, seeds.pending.Email, seeds.rejected.Email, seeds.fresh.Email)
	if !strings.Contains(body, strconv.FormatInt(seeds.expired.ID, 10)) {
		t.Fatalf("missing expired external_reference %d", seeds.expired.ID)
	}
	assertNoForbiddenControls(t, body)
}

func TestAdminAllOrdersFilter(t *testing.T) {
	now := openNow()
	mux, db, _ := testMuxSender(t, nil, nil, now)
	seeds := seedAdminOrders(t, db, now)
	body := getAdmin(t, mux, "/admin?filter=all")

	assertContains(t, body,
		seeds.unfulfilled.Email, seeds.failed.Email, seeds.stuck.Email, seeds.expired.Email,
		seeds.sent.Email, seeds.pending.Email, seeds.rejected.Email, seeds.fresh.Email,
	)
	if !strings.Contains(body, strconv.FormatInt(seeds.expired.ID, 10)) {
		t.Fatalf("missing expired external_reference %d", seeds.expired.ID)
	}
	if !strings.Contains(body, seeds.failed.EmailLastError.String) {
		t.Fatalf("missing email_last_error")
	}
	if !strings.Contains(body, "Reenviar") {
		t.Fatal("missing resend on failed row")
	}
	assertNoForbiddenControls(t, body)
}

func TestAdminUnknownFilterIsAttention(t *testing.T) {
	now := openNow()
	mux, db, _ := testMuxSender(t, nil, nil, now)
	seeds := seedAdminOrders(t, db, now)
	body := getAdmin(t, mux, "/admin?filter=other")
	assertContains(t, body, seeds.expired.Email)
	assertNotContains(t, body, seeds.pending.Email)
}

func TestAdminExpiresStalePending(t *testing.T) {
	now := openNow()
	charger := &fakeCharger{}
	mux, db, _ := testMuxSender(t, charger, nil, now)
	ctx := context.Background()
	stale, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Stale",
		FamilyName:        "Hold",
		Email:             "stale-hold@example.com",
		Age:               40,
		Quantity:          1,
		UnitPriceCents:    25000,
		TotalCents:        25000,
		Status:            "pending_payment",
		FulfillmentStatus: "pending",
		EmailStatus:       "not_sent",
		ReservedUntil:     now.UTC().Add(-time.Minute).Format(time.RFC3339),
		PollToken:         "poll-admin-stale",
		CheckoutKey:       "ck-admin-stale",
	})
	if err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
		OrderID: stale.ID,
		Status:  "reserved",
	}); err != nil {
		t.Fatalf("InsertTicket: %v", err)
	}

	body := getAdmin(t, mux, "/admin")
	if len(charger.calls) != 0 {
		t.Fatalf("checkout calls = %d", len(charger.calls))
	}
	got, err := db.Queries.GetOrderByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.Status != "expired" {
		t.Fatalf("status = %q", got.Status)
	}
	tickets, err := db.Queries.ListTicketsByOrder(ctx, stale.ID)
	if err != nil {
		t.Fatalf("ListTicketsByOrder: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("tickets = %d", len(tickets))
	}
	if !strings.Contains(body, stale.Email) {
		t.Fatalf("expired row missing from attention: %s", body)
	}
}

func TestAdminResendFailedCallsSender(t *testing.T) {
	now := openNow()
	sender := &fakeSender{now: func() time.Time { return now }}
	mux, db, _ := testMuxSender(t, nil, sender, now)
	sender.db = db
	seeds := seedAdminOrders(t, db, now)

	rec := postAdminResend(t, mux, seeds.failed.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("Location = %q", loc)
	}
	if len(sender.orders) != 1 || sender.orders[0].ID != seeds.failed.ID {
		t.Fatalf("sends = %v", sender.orders)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), seeds.failed.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "sent" {
		t.Fatalf("email_status = %q", got.EmailStatus)
	}
}

func TestAdminResendUnfulfilledDoesNotSend(t *testing.T) {
	now := openNow()
	sender := &fakeSender{now: func() time.Time { return now }}
	mux, db, _ := testMuxSender(t, nil, sender, now)
	sender.db = db
	seeds := seedAdminOrders(t, db, now)

	postAdminResend(t, mux, seeds.unfulfilled.ID)
	if len(sender.orders) != 0 {
		t.Fatalf("sends = %v", sender.orders)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), seeds.unfulfilled.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "not_sent" {
		t.Fatalf("email_status = %q", got.EmailStatus)
	}
}

func TestAdminResendRequiresSession(t *testing.T) {
	now := openNow()
	mux, _ := testMux(t, nil, now)
	form := url.Values{"order_id": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/resend", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?next=/admin/resend" {
		t.Fatalf("Location = %q", loc)
	}
}

func getAdmin(t *testing.T, mux http.Handler, path string) string {
	t.Helper()
	c := loginCookie(t, mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d body %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func postAdminResend(t *testing.T, mux http.Handler, orderID int64) *httptest.ResponseRecorder {
	t.Helper()
	c := loginCookie(t, mux)
	form := url.Values{"order_id": {strconv.FormatInt(orderID, 10)}}
	req := httptest.NewRequest(http.MethodPost, "/admin/resend", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func seedAdminOrders(t *testing.T, db *database.DB, now time.Time) adminSeeds {
	t.Helper()
	var seeds adminSeeds
	seeds.unfulfilled = insertAdminOrder(t, db, adminOrderParams{
		email: "unfulfilled@example.com", given: "Unful", key: "unful",
		status: "paid", fulfillment: "unfulfilled", emailStatus: "not_sent",
		until: reservedUntilStamp(now),
	})
	seeds.failed = insertAdminOrder(t, db, adminOrderParams{
		email: "failed@example.com", given: "Fail", key: "fail",
		status: "paid", fulfillment: "fulfilled", emailStatus: "failed",
		until: reservedUntilStamp(now), errMsg: "smtp timeout",
	})
	seeds.stuck = insertAdminOrder(t, db, adminOrderParams{
		email: "stuck@example.com", given: "Stuck", key: "stuck",
		status: "paid", fulfillment: "fulfilled", emailStatus: "not_sent",
		until: reservedUntilStamp(now), updated: sqliteNow(now.Add(-11 * time.Minute)),
	})
	seeds.expired = insertAdminOrder(t, db, adminOrderParams{
		email: "expired@example.com", given: "Exp", key: "exp",
		status: "expired", fulfillment: "pending", emailStatus: "not_sent",
		until: now.UTC().Add(-time.Hour).Format(time.RFC3339),
	})
	seeds.sent = insertAdminOrder(t, db, adminOrderParams{
		email: "sent@example.com", given: "Sent", key: "sent",
		status: "paid", fulfillment: "fulfilled", emailStatus: "sent",
		until: reservedUntilStamp(now), mpID: "mp-sent",
	})
	seeds.pending = insertAdminOrder(t, db, adminOrderParams{
		email: "pending@example.com", given: "Pend", key: "pend",
		status: "pending_payment", fulfillment: "pending", emailStatus: "not_sent",
		until: reservedUntilStamp(now),
	})
	seeds.rejected = insertAdminOrder(t, db, adminOrderParams{
		email: "rejected@example.com", given: "Rej", key: "rej",
		status: "rejected", fulfillment: "pending", emailStatus: "not_sent",
		until: reservedUntilStamp(now),
	})
	seeds.fresh = insertAdminOrder(t, db, adminOrderParams{
		email: "fresh@example.com", given: "Fresh", key: "fresh",
		status: "paid", fulfillment: "fulfilled", emailStatus: "not_sent",
		until: reservedUntilStamp(now), updated: sqliteNow(now.Add(-time.Minute)),
	})
	return seeds
}

type adminOrderParams struct {
	email, given, key, status, fulfillment, emailStatus, until, updated, errMsg, mpID string
}

func insertAdminOrder(t *testing.T, db *database.DB, p adminOrderParams) sqlc.Order {
	t.Helper()
	ctx := context.Background()
	order, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         p.given,
		FamilyName:        "Buyer",
		Email:             p.email,
		Age:               30,
		Quantity:          2,
		UnitPriceCents:    25000,
		TotalCents:        50000,
		Status:            p.status,
		FulfillmentStatus: p.fulfillment,
		EmailStatus:       p.emailStatus,
		ReservedUntil:     p.until,
		PollToken:         "poll-" + p.key,
		CheckoutKey:       "ck-" + p.key,
	})
	if err != nil {
		t.Fatalf("InsertOrder %s: %v", p.key, err)
	}
	if p.mpID != "" {
		if err := db.Queries.UpdateOrderMpID(ctx, sqlc.UpdateOrderMpIDParams{
			MpOrderID: sql.NullString{String: p.mpID, Valid: true},
			ID:        order.ID,
		}); err != nil {
			t.Fatalf("mp id: %v", err)
		}
		order.MpOrderID = sql.NullString{String: p.mpID, Valid: true}
	}
	if p.updated != "" || p.errMsg != "" {
		var last sql.NullString
		if p.errMsg != "" {
			last = sql.NullString{String: p.errMsg, Valid: true}
		}
		updated := p.updated
		if updated == "" {
			updated = sqliteNow(openNow())
		}
		if err := db.Queries.UpdateOrderEmailStatus(ctx, sqlc.UpdateOrderEmailStatusParams{
			EmailStatus:    p.emailStatus,
			EmailLastError: last,
			UpdatedAt:      updated,
			ID:             order.ID,
		}); err != nil {
			t.Fatalf("email status: %v", err)
		}
		order.EmailLastError = last
		order.UpdatedAt = updated
	}
	return order
}

func assertContains(t *testing.T, body string, emails ...string) {
	t.Helper()
	for _, email := range emails {
		if !strings.Contains(body, email) {
			t.Fatalf("missing %q in %s", email, body)
		}
	}
}

func assertNotContains(t *testing.T, body string, emails ...string) {
	t.Helper()
	for _, email := range emails {
		if strings.Contains(body, email) {
			t.Fatalf("unexpected %q in %s", email, body)
		}
	}
}

func assertNoForbiddenControls(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, bad := range []string{"dismiss", "descartar", "emitir", "mint", "/admin/issue"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("forbidden control %q in %s", bad, body)
		}
	}
}
