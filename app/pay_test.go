package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"raices/database"
	"raices/database/sqlc"
	"raices/env"
	"raices/payment"
)

type testLogger struct{}

func (testLogger) Info(string, ...any)    {}
func (testLogger) Error(string, ...any)   {}
func (testLogger) Warning(string, ...any) {}

type fakeCharger struct {
	result payment.Result
	calls  []payment.Charge
}

func (f *fakeCharger) Create(_ context.Context, charge payment.Charge) payment.Result {
	f.calls = append(f.calls, charge)
	return f.result
}

func setPayEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "unused.db"))
	t.Setenv("APP_BASE_URL", "http://localhost:3000")
	t.Setenv("TICKET_PRICE", "250")
	t.Setenv("MP_ACCESS_TOKEN", "mp-access-token")
	t.Setenv("MP_PUBLIC_KEY", "mp-public-key")
	t.Setenv("MP_WEBHOOK_SECRET", "mp-webhook-secret")
	t.Setenv("RESEND_API_KEY", "resend-api-key")
	t.Setenv("RESEND_FROM", "from@example.com")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "admin-password")
	if err := env.Sync(); err != nil {
		t.Fatalf("env.Sync: %v", err)
	}
}

func testMux(t *testing.T, charger *fakeCharger, now time.Time) (*http.ServeMux, *database.DB) {
	t.Helper()
	mux, db, _ := testMuxSender(t, charger, nil, now)
	return mux, db
}

func testMuxSender(t *testing.T, charger *fakeCharger, sender Sender, now time.Time) (*http.ServeMux, *database.DB, Sender) {
	t.Helper()
	setPayEnv(t)
	db, err := database.Open(filepath.Join(t.TempDir(), "raices.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	Routes(Dependencies{
		Mux:     mux,
		DB:      db,
		Logger:  testLogger{},
		Charger: charger,
		Sender:  sender,
		Now:     func() time.Time { return now },
	})
	return mux, db, sender
}

func payValues(checkoutKey string) url.Values {
	return url.Values{
		"qty":               {"2"},
		"given_name":        {"Ada"},
		"family_name":       {"Lovelace"},
		"email":             {"ada@example.com"},
		"email_confirm":     {"ADA@example.com"},
		"age":               {"36"},
		"church":            {"  "},
		"token":             {"card-token"},
		"checkout_key":      {checkoutKey},
		"payment_method_id": {"visa"},
	}
}

func postPay(t *testing.T, mux *http.ServeMux, vals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/register/pay", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func hiddenValue(body, name string) string {
	re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

func openNow() time.Time {
	return time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
}

func TestCutoffReservesThenBlocks(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	justOpen := time.Date(2026, 10, 31, 5, 59, 59, 0, time.UTC)
	mux, db := testMux(t, charger, justOpen)
	rec := postPay(t, mux, payValues("ck-open"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	n, err := db.Queries.CountOrders(context.Background())
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if n != 1 {
		t.Fatalf("orders = %d, want 1", n)
	}

	closed := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	mux2, db2 := testMux(t, charger, closed)
	rec2 := postPay(t, mux2, payValues("ck-closed"))
	if !strings.Contains(rec2.Body.String(), "Las compras cerraron.") {
		t.Fatalf("body %s", rec2.Body.String())
	}
	n, err = db2.Queries.CountOrders(context.Background())
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if n != 0 {
		t.Fatalf("orders after cutoff = %d, want 0", n)
	}
}

func TestPaidNotSentKeepsPolling(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	order := sqlc.Order{
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "not_sent",
		PollToken:         "poll-secret",
		UpdatedAt:         now.Add(-time.Minute).Format("2006-01-02 15:04:05"),
		ReservedUntil:     now.Add(10 * time.Minute).Format(time.RFC3339),
	}
	view := orderPanel(order, now, "")
	if view.Message != msgConfirming || !strings.Contains(view.PollPath, "poll_token=poll-secret") || view.Redirect != "" {
		t.Fatalf("%+v", view)
	}
	order.EmailStatus = "sent"
	view = orderPanel(order, now, "")
	if view.Message != msgPaidSent || view.PollPath != "" || !strings.Contains(view.Redirect, "/register/confirmed?poll_token=poll-secret") {
		t.Fatalf("%+v", view)
	}
}

func TestProcessedLeavesReservedPending(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{
		Kind:         payment.KindOrder,
		MPOrderID:    "mp-sync-ok",
		Status:       "processed",
		StatusDetail: "accredited",
	}}
	mux, db := testMux(t, charger, openNow())
	vals := payValues("ck-processed")
	rec := postPay(t, mux, vals)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), msgConfirming) {
		t.Fatalf("body %s", rec.Body.String())
	}
	ctx := context.Background()
	order, err := db.Queries.GetOrderByCheckoutKey(ctx, "ck-processed")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "pending_payment" {
		t.Fatalf("status %q", order.Status)
	}
	if !order.MpOrderID.Valid || order.MpOrderID.String != "mp-sync-ok" {
		t.Fatalf("mp_order_id %+v", order.MpOrderID)
	}
	tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("tickets %d", len(tickets))
	}
	for _, tk := range tickets {
		if tk.Status != "reserved" || tk.Token.Valid {
			t.Fatalf("ticket %+v", tk)
		}
	}
	if len(charger.calls) != 1 {
		t.Fatalf("charges %d", len(charger.calls))
	}
	if charger.calls[0].IdempotencyKey != "ck-processed" {
		t.Fatalf("idempotency %q", charger.calls[0].IdempotencyKey)
	}
	if charger.calls[0].LocalOrderID != strconv.FormatInt(order.ID, 10) {
		t.Fatalf("external reference %q want %d", charger.calls[0].LocalOrderID, order.ID)
	}

	rec2 := postPay(t, mux, vals)
	if rec2.Code != http.StatusOK {
		t.Fatalf("resubmit status %d", rec2.Code)
	}
	n, err := db.Queries.CountOrders(ctx)
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if n != 1 || len(charger.calls) != 1 {
		t.Fatalf("orders=%d charges=%d", n, len(charger.calls))
	}
}

func TestUnknownKeepsReservation(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	mux, db := testMux(t, charger, openNow())
	postPay(t, mux, payValues("ck-unknown"))
	order, err := db.Queries.GetOrderByCheckoutKey(context.Background(), "ck-unknown")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "pending_payment" || order.MpOrderID.Valid {
		t.Fatalf("order %+v", order)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 2 {
		t.Fatalf("tickets %d", len(tickets))
	}
	for _, tk := range tickets {
		if tk.Status != "reserved" || tk.Token.Valid {
			t.Fatalf("ticket %+v", tk)
		}
	}
}

func TestFailedOrderReleasesSeats(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{
		Kind:         payment.KindOrder,
		MPOrderID:    "mp-fail",
		Status:       "failed",
		StatusDetail: "failed",
	}}
	mux, db := testMux(t, charger, openNow())
	rec := postPay(t, mux, payValues("ck-fail"))
	body := rec.Body.String()
	if !strings.Contains(body, msgRejected) {
		t.Fatalf("body %s", body)
	}
	newKey := hiddenValue(body, "checkout_key")
	if newKey == "" || newKey == "ck-fail" {
		t.Fatalf("checkout_key %q", newKey)
	}
	if hiddenValue(body, "token") != "" {
		t.Fatalf("token not empty")
	}
	ctx := context.Background()
	order, err := db.Queries.GetOrderByCheckoutKey(ctx, "ck-fail")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "rejected" {
		t.Fatalf("status %q", order.Status)
	}
	tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("tickets %d", len(tickets))
	}
}

func TestNoOrderReleasesSeats(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindNoOrder}}
	mux, db := testMux(t, charger, openNow())
	rec := postPay(t, mux, payValues("ck-no-order"))
	body := rec.Body.String()
	if !strings.Contains(body, msgRejected) {
		t.Fatalf("body %s", body)
	}
	newKey := hiddenValue(body, "checkout_key")
	if newKey == "" || newKey == "ck-no-order" {
		t.Fatalf("checkout_key %q", newKey)
	}
	if hiddenValue(body, "token") != "" {
		t.Fatalf("token not empty")
	}
	order, err := db.Queries.GetOrderByCheckoutKey(context.Background(), "ck-no-order")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "rejected" {
		t.Fatalf("status %q", order.Status)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("tickets %d", len(tickets))
	}
}

func TestCapacityIncludesUsed(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	mux, db := testMux(t, charger, openNow())
	ctx := context.Background()
	seed, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Used",
		FamilyName:        "Seat",
		Email:             "used@example.com",
		Age:               30,
		Church:            sql.NullString{},
		Quantity:          1,
		UnitPriceCents:    25000,
		TotalCents:        25000,
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "sent",
		ReservedUntil:     reservedUntilStamp(openNow()),
		PollToken:         "poll-used",
		CheckoutKey:       "ck-used-seed",
	})
	if err != nil {
		t.Fatalf("seed order: %v", err)
	}
	for i := 0; i < ticketCapacity; i++ {
		if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
			OrderID: seed.ID,
			Token:   sql.NullString{String: fmt.Sprintf("used-%d", i), Valid: true},
			Status:  "used",
		}); err != nil {
			t.Fatalf("used ticket %d: %v", i, err)
		}
	}
	rec := postPay(t, mux, payValues("ck-full"))
	body := rec.Body.String()
	if !strings.Contains(body, "Quedan 0 lugares.") {
		t.Fatalf("body %s", body)
	}
	if len(charger.calls) != 0 {
		t.Fatalf("charger called %d times", len(charger.calls))
	}
	n, err := db.Queries.CountOrders(ctx)
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if n != 1 {
		t.Fatalf("orders %d want seed only", n)
	}
}

func TestChallengeIframeAndExternalLink(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{
		Kind:         payment.KindOrder,
		MPOrderID:    "mp-ch",
		Status:       "action_required",
		StatusDetail: "pending_challenge",
		ChallengeURL: "https://www.mercadopago.com.mx/auth/card/validation/x",
	}}
	mux, db := testMux(t, charger, openNow())
	rec := postPay(t, mux, payValues("ck-iframe"))
	body := rec.Body.String()
	if !strings.Contains(body, `<iframe`) || !strings.Contains(body, "www.mercadopago.com.mx") {
		t.Fatalf("expected iframe, body %s", body)
	}
	order, err := db.Queries.GetOrderByCheckoutKey(context.Background(), "ck-iframe")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "pending_payment" {
		t.Fatalf("status %q", order.Status)
	}

	charger.result.ChallengeURL = "https://acs.example/challenge"
	mux2, _ := testMux(t, charger, openNow())
	rec2 := postPay(t, mux2, payValues("ck-link"))
	body2 := rec2.Body.String()
	if strings.Contains(body2, "<iframe") {
		t.Fatalf("unexpected iframe: %s", body2)
	}
	if !strings.Contains(body2, `target="_top"`) || !strings.Contains(body2, "Continuar la verificación del banco") {
		t.Fatalf("expected top-level link, body %s", body2)
	}
	if !strings.Contains(body2, "https://acs.example/challenge") {
		t.Fatalf("missing challenge href: %s", body2)
	}
}

func TestPollToken(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	mux, db := testMux(t, charger, openNow())
	postPay(t, mux, payValues("ck-poll"))
	order, err := db.Queries.GetOrderByCheckoutKey(context.Background(), "ck-poll")
	if err != nil {
		t.Fatalf("order: %v", err)
	}

	const bogus = "bogus-poll-token-xyz"
	bad := httptest.NewRequest(http.MethodGet, "/register/status?poll_token="+bogus, nil)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusNotFound {
		t.Fatalf("status %d", badRec.Code)
	}
	if strings.Contains(badRec.Body.String(), bogus) {
		t.Fatalf("echoed poll_token: %s", badRec.Body.String())
	}
	if !strings.Contains(badRec.Body.String(), msgUnknownPay) {
		t.Fatalf("body %s", badRec.Body.String())
	}

	okReq := httptest.NewRequest(http.MethodGet, "/register/status?poll_token="+url.QueryEscape(order.PollToken), nil)
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", okRec.Code, okRec.Body.String())
	}
	got := okRec.Body.String()
	if !strings.Contains(got, msgConfirming) {
		t.Fatalf("body %s", got)
	}
	if strings.Contains(got, "ada@example.com") || strings.Contains(got, "Ada") {
		t.Fatalf("leaked buyer: %s", got)
	}
}

func TestExpireStaleOnPoll(t *testing.T) {
	now := openNow()
	mux, db := testMux(t, &fakeCharger{}, now)
	ctx := context.Background()
	stale, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Stale",
		FamilyName:        "Buyer",
		Email:             "stale@example.com",
		Age:               40,
		Quantity:          1,
		UnitPriceCents:    25000,
		TotalCents:        25000,
		Status:            "pending_payment",
		FulfillmentStatus: "pending",
		EmailStatus:       "not_sent",
		ReservedUntil:     now.UTC().Add(-time.Minute).Format(time.RFC3339),
		PollToken:         "poll-stale",
		CheckoutKey:       "ck-stale",
	})
	if err != nil {
		t.Fatalf("stale order: %v", err)
	}
	if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
		OrderID: stale.ID,
		Status:  "reserved",
	}); err != nil {
		t.Fatalf("reserved ticket: %v", err)
	}
	live, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Live",
		FamilyName:        "Buyer",
		Email:             "live@example.com",
		Age:               41,
		Quantity:          1,
		UnitPriceCents:    25000,
		TotalCents:        25000,
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "sent",
		ReservedUntil:     reservedUntilStamp(now),
		PollToken:         "poll-live",
		CheckoutKey:       "ck-live",
	})
	if err != nil {
		t.Fatalf("live order: %v", err)
	}
	if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
		OrderID: live.ID,
		Token:   sql.NullString{String: "issued-token", Valid: true},
		Status:  "issued",
	}); err != nil {
		t.Fatalf("issued ticket: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/register/status?poll_token=poll-stale", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	got, err := db.Queries.GetOrderByID(ctx, stale.ID)
	if err != nil {
		t.Fatalf("reload stale: %v", err)
	}
	if got.Status != "expired" {
		t.Fatalf("stale status %q", got.Status)
	}
	staleTickets, err := db.Queries.ListTicketsByOrder(ctx, stale.ID)
	if err != nil {
		t.Fatalf("stale tickets: %v", err)
	}
	if len(staleTickets) != 0 {
		t.Fatalf("stale tickets %d", len(staleTickets))
	}
	liveTickets, err := db.Queries.ListTicketsByOrder(ctx, live.ID)
	if err != nil {
		t.Fatalf("live tickets: %v", err)
	}
	if len(liveTickets) != 1 || liveTickets[0].Status != "issued" {
		t.Fatalf("issued ticket %+v", liveTickets)
	}
}

func TestEmptyTokenDoesNotInsert(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	mux, db := testMux(t, charger, openNow())
	vals := payValues("ck-empty-token")
	vals.Set("token", "")
	postPay(t, mux, vals)
	n, err := db.Queries.CountOrders(context.Background())
	if err != nil {
		t.Fatalf("CountOrders: %v", err)
	}
	if n != 0 || len(charger.calls) != 0 {
		t.Fatalf("orders=%d charges=%d", n, len(charger.calls))
	}
}

func TestFormatCentsViaChargeAmount(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{Kind: payment.KindUnknown}}
	mux, _ := testMux(t, charger, openNow())
	postPay(t, mux, payValues("ck-amount"))
	if len(charger.calls) != 1 {
		t.Fatalf("calls %d", len(charger.calls))
	}
	if charger.calls[0].TotalCents != 50000 {
		t.Fatalf("total %d", charger.calls[0].TotalCents)
	}
}

func TestStatusRedirectsAfterTicketsSent(t *testing.T) {
	now := openNow()
	mux, db := testMux(t, nil, now)
	ctx := context.Background()
	order, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Ada",
		FamilyName:        "Lovelace",
		Email:             "ada@example.com",
		Age:               36,
		Quantity:          2,
		UnitPriceCents:    25000,
		TotalCents:        50000,
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "sent",
		ReservedUntil:     now.UTC().Add(15 * time.Minute).Format(time.RFC3339),
		PollToken:         "poll-sent",
		CheckoutKey:       "ck-sent",
	})
	if err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/register/status?poll_token="+order.PollToken, nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("htmx status %d", rec.Code)
	}
	loc := rec.Header().Get("HX-Redirect")
	if !strings.Contains(loc, "/register/confirmed?poll_token=poll-sent") {
		t.Fatalf("HX-Redirect %q", loc)
	}

	plain := httptest.NewRequest(http.MethodGet, "/register/status?poll_token="+order.PollToken, nil)
	plainRec := httptest.NewRecorder()
	mux.ServeHTTP(plainRec, plain)
	if plainRec.Code != http.StatusSeeOther || plainRec.Header().Get("Location") != loc {
		t.Fatalf("status %d location %q", plainRec.Code, plainRec.Header().Get("Location"))
	}

	page := httptest.NewRequest(http.MethodGet, loc, nil)
	pageRec := httptest.NewRecorder()
	mux.ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("confirmed %d", pageRec.Code)
	}
	body := pageRec.Body.String()
	for _, want := range []string{"Pago confirmado", "Ada Lovelace", "ada@example.com", "Volver al registro", "Cerrar página", "Mex$500.00"} {
		if !strings.Contains(body, want) {
			t.Fatalf("confirmed page missing %q: %s", want, body)
		}
	}
}
