package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mporder "github.com/mercadopago/sdk-go/pkg/order"

	"raices/database"
	"raices/database/sqlc"
	"raices/env"
	"raices/payment"
)

type fakeFetcher struct {
	resp  *mporder.Response
	err   error
	calls []string
}

func (f *fakeFetcher) Get(_ context.Context, id string) (*mporder.Response, error) {
	f.calls = append(f.calls, id)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeSender struct {
	orders []sqlc.Order
	db     *database.DB
	now    func() time.Time
}

func (f *fakeSender) Send(ctx context.Context, order sqlc.Order) error {
	f.orders = append(f.orders, order)
	if f.db == nil {
		return nil
	}
	stamp := sqliteNow(time.Now())
	if f.now != nil {
		stamp = sqliteNow(f.now())
	}
	return f.db.Queries.UpdateOrderEmailStatus(ctx, sqlc.UpdateOrderEmailStatusParams{
		EmailStatus:    "sent",
		EmailLastError: sql.NullString{},
		UpdatedAt:      stamp,
		ID:             order.ID,
	})
}

func webhookMux(t *testing.T, fetch *fakeFetcher, sender *fakeSender, now time.Time) (*http.ServeMux, *database.DB) {
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
		Charger: &fakeCharger{},
		Fetcher: fetch,
		Sender:  sender,
		Now:     func() time.Time { return now },
	})
	return mux, db
}

func mpSign(secret, id, requestID, ts string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(webhookManifest(id, requestID, ts)))
	return "ts=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func postMPWebhook(t *testing.T, mux http.Handler, queryID, queryType, jsonType, jsonID, reqID, sig string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{}
	if queryID != "" {
		q.Set("data.id", queryID)
	}
	if queryType != "" {
		q.Set("type", queryType)
	}
	body := fmt.Sprintf(`{"type":%q,"data":{"id":%q}}`, jsonType, jsonID)
	id := strings.ToLower(queryID)
	if id == "" {
		id = strings.ToLower(jsonID)
	}
	ts := "1700000000"
	if sig == "" {
		sig = mpSign(env.MPWebhookSecret, id, reqID, ts)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/mercadopago?"+q.Encode(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-signature", sig)
	if reqID != "" {
		req.Header.Set("x-request-id", reqID)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func successResp(localID int64, mpID, amount, currency string) *mporder.Response {
	return &mporder.Response{
		ID:                mpID,
		ExternalReference: strconv.FormatInt(localID, 10),
		Status:            "processed",
		StatusDetail:      "accredited",
		TotalAmount:       amount,
		Currency:          currency,
	}
}

func seedOrder(t *testing.T, db *database.DB, p sqlc.InsertOrderParams, reserved int, mpID string) sqlc.Order {
	t.Helper()
	ctx := context.Background()
	order, err := db.Queries.InsertOrder(ctx, p)
	if err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	for i := 0; i < reserved; i++ {
		if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
			OrderID: order.ID,
			Status:  "reserved",
		}); err != nil {
			t.Fatalf("reserved ticket: %v", err)
		}
	}
	if mpID != "" {
		if err := db.Queries.UpdateOrderMpID(ctx, sqlc.UpdateOrderMpIDParams{
			MpOrderID: sql.NullString{String: mpID, Valid: true},
			ID:        order.ID,
		}); err != nil {
			t.Fatalf("mp id: %v", err)
		}
		order.MpOrderID = sql.NullString{String: mpID, Valid: true}
	}
	return order
}

func pendingParams(now time.Time, qty int, key string) sqlc.InsertOrderParams {
	return sqlc.InsertOrderParams{
		GivenName:         "Ada",
		FamilyName:        "Lovelace",
		Email:             "ada@example.com",
		Age:               36,
		Quantity:          int64(qty),
		UnitPriceCents:    25000,
		TotalCents:        int64(25000 * qty),
		Status:            "pending_payment",
		FulfillmentStatus: "pending",
		EmailStatus:       "not_sent",
		ReservedUntil:     reservedUntilStamp(now),
		PollToken:         "poll-" + key,
		CheckoutKey:       "ck-" + key,
	}
}

func fillUsed(t *testing.T, db *database.DB, now time.Time, n int, key string) {
	t.Helper()
	ctx := context.Background()
	seed, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Used",
		FamilyName:        "Seats",
		Email:             "used@example.com",
		Age:               30,
		Quantity:          1,
		UnitPriceCents:    25000,
		TotalCents:        25000,
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "sent",
		ReservedUntil:     reservedUntilStamp(now),
		PollToken:         "poll-used-" + key,
		CheckoutKey:       "ck-used-" + key,
	})
	if err != nil {
		t.Fatalf("used order: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := db.Queries.InsertTicket(ctx, sqlc.InsertTicketParams{
			OrderID: seed.ID,
			Token:   sql.NullString{String: fmt.Sprintf("%s-%d", key, i), Valid: true},
			Status:  "used",
		}); err != nil {
			t.Fatalf("used ticket %d: %v", i, err)
		}
	}
}

func assertIssued(t *testing.T, tickets []sqlc.Ticket, n int) {
	t.Helper()
	if len(tickets) != n {
		t.Fatalf("tickets %d want %d", len(tickets), n)
	}
	seen := map[string]bool{}
	for _, tk := range tickets {
		if tk.Status != "issued" || !tk.Token.Valid {
			t.Fatalf("ticket %+v", tk)
		}
		raw, err := base64.RawURLEncoding.DecodeString(tk.Token.String)
		if err != nil || len(raw) != 32 {
			t.Fatalf("token %q", tk.Token.String)
		}
		if seen[tk.Token.String] {
			t.Fatalf("duplicate token")
		}
		seen[tk.Token.String] = true
	}
}

func TestDuplicateWebhookSuccessIssuesOnce(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 2, "dup"), 2, "mp-dup")
	fetch.resp = successResp(order.ID, "mp-dup", "500.00", "MXN")

	rec1 := postMPWebhook(t, mux, "mp-dup", "order", "order", "mp-dup", "req-1", "")
	rec2 := postMPWebhook(t, mux, "mp-dup", "order", "order", "mp-dup", "req-2", "")
	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatalf("status %d %d", rec1.Code, rec2.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	assertIssued(t, tickets, 2)
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestWebhookThenMpOrderIDUpdate(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 1, "race"), 1, "")
	fetch.resp = successResp(order.ID, "mp-race", "250.00", "MXN")

	rec := postMPWebhook(t, mux, "mp-race", "order", "order", "mp-race", "req-race", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if err := db.Queries.UpdateOrderMpID(context.Background(), sqlc.UpdateOrderMpIDParams{
		MpOrderID: sql.NullString{String: "mp-race", Valid: true},
		ID:        order.ID,
	}); err != nil {
		t.Fatalf("mp update: %v", err)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	if !got.MpOrderID.Valid || got.MpOrderID.String != "mp-race" {
		t.Fatalf("mp_order_id %+v", got.MpOrderID)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	assertIssued(t, tickets, 1)
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestFailureOnPaidIsIgnored(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	p := pendingParams(now, 1, "paid-fail")
	p.Status = "paid"
	p.FulfillmentStatus = "fulfilled"
	p.EmailStatus = "sent"
	order := seedOrder(t, db, p, 0, "mp-paid-fail")
	if _, err := db.Queries.InsertTicket(context.Background(), sqlc.InsertTicketParams{
		OrderID: order.ID,
		Token:   sql.NullString{String: "keep-me", Valid: true},
		Status:  "issued",
	}); err != nil {
		t.Fatalf("ticket: %v", err)
	}
	fetch.resp = &mporder.Response{
		ID:                "mp-paid-fail",
		ExternalReference: strconv.FormatInt(order.ID, 10),
		Status:            "failed",
		StatusDetail:      "failed",
		TotalAmount:       "250.00",
		Currency:          "MXN",
	}
	rec := postMPWebhook(t, mux, "mp-paid-fail", "order", "order", "mp-paid-fail", "req-pf", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Token.String != "keep-me" {
		t.Fatalf("tickets %+v", tickets)
	}
	if len(sender.orders) != 0 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestLiveReservationIssuesWithoutCapacityCheck(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	fillUsed(t, db, now, ticketCapacity, "full")
	order := seedOrder(t, db, pendingParams(now, 2, "live-cap"), 2, "mp-live")
	fetch.resp = successResp(order.ID, "mp-live", "500.00", "MXN")

	rec := postMPWebhook(t, mux, "mp-live", "order", "order", "mp-live", "req-live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	assertIssued(t, tickets, 2)
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestExpiredReinsertAndUnfulfilled(t *testing.T) {
	now := openNow()
	ctx := context.Background()

	t.Run("seats remain", func(t *testing.T) {
		fetch := &fakeFetcher{}
		sender := &fakeSender{}
		mux, db := webhookMux(t, fetch, sender, now)
		p := pendingParams(now, 2, "exp-ok")
		p.Status = "expired"
		order := seedOrder(t, db, p, 0, "mp-exp-ok")
		fetch.resp = successResp(order.ID, "mp-exp-ok", "500.00", "MXN")
		rec := postMPWebhook(t, mux, "mp-exp-ok", "order", "order", "mp-exp-ok", "req-exp-ok", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		got, err := db.Queries.GetOrderByID(ctx, order.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
			t.Fatalf("order %+v", got)
		}
		tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
		if err != nil {
			t.Fatalf("tickets: %v", err)
		}
		assertIssued(t, tickets, 2)
		if len(sender.orders) != 1 {
			t.Fatalf("sends %d", len(sender.orders))
		}
	})

	t.Run("no seats", func(t *testing.T) {
		fetch := &fakeFetcher{}
		sender := &fakeSender{}
		mux, db := webhookMux(t, fetch, sender, now)
		fillUsed(t, db, now, ticketCapacity, "exp-full")
		p := pendingParams(now, 2, "exp-full")
		p.Status = "expired"
		order := seedOrder(t, db, p, 0, "mp-exp-full")
		fetch.resp = successResp(order.ID, "mp-exp-full", "500.00", "MXN")
		rec := postMPWebhook(t, mux, "mp-exp-full", "order", "order", "mp-exp-full", "req-exp-full", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		got, err := db.Queries.GetOrderByID(ctx, order.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		if got.Status != "paid" || got.FulfillmentStatus != "unfulfilled" {
			t.Fatalf("order %+v", got)
		}
		tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
		if err != nil {
			t.Fatalf("tickets: %v", err)
		}
		if len(tickets) != 0 {
			t.Fatalf("tickets %d", len(tickets))
		}
		if len(sender.orders) != 0 {
			t.Fatalf("sends %d", len(sender.orders))
		}
	})
}

func TestAmountAndCurrencyMismatch(t *testing.T) {
	now := openNow()
	ctx := context.Background()
	cases := []struct {
		name, amount, currency string
	}{
		{"cents", "250.01", "MXN"},
		{"usd", "250.00", "USD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fetch := &fakeFetcher{}
			sender := &fakeSender{}
			mux, db := webhookMux(t, fetch, sender, now)
			order := seedOrder(t, db, pendingParams(now, 1, "amt-"+tc.name), 1, "mp-amt-"+tc.name)
			fetch.resp = successResp(order.ID, "mp-amt-"+tc.name, tc.amount, tc.currency)
			rec := postMPWebhook(t, mux, "mp-amt-"+tc.name, "order", "order", "mp-amt-"+tc.name, "req-amt", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			got, err := db.Queries.GetOrderByID(ctx, order.ID)
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if got.Status != "paid" || got.FulfillmentStatus != "unfulfilled" {
				t.Fatalf("order %+v", got)
			}
			tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
			if err != nil {
				t.Fatalf("tickets: %v", err)
			}
			if len(tickets) != 1 || tickets[0].Status != "reserved" || tickets[0].Token.Valid {
				t.Fatalf("tickets %+v", tickets)
			}
			if len(sender.orders) != 0 {
				t.Fatalf("sends %d", len(sender.orders))
			}
		})
	}

	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 1, "amt-ok"), 1, "mp-amt-ok")
	fetch.resp = successResp(order.ID, "mp-amt-ok", "250.00", "mxn")
	rec := postMPWebhook(t, mux, "mp-amt-ok", "order", "order", "mp-amt-ok", "req-ok", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	tickets, err := db.Queries.ListTicketsByOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	assertIssued(t, tickets, 1)
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestCrashShapedDoesNotSend(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	p := pendingParams(now, 1, "crash")
	p.Status = "paid"
	p.FulfillmentStatus = "fulfilled"
	p.EmailStatus = "not_sent"
	order := seedOrder(t, db, p, 0, "mp-crash")
	if _, err := db.Queries.InsertTicket(context.Background(), sqlc.InsertTicketParams{
		OrderID: order.ID,
		Token:   sql.NullString{String: "already-issued", Valid: true},
		Status:  "issued",
	}); err != nil {
		t.Fatalf("ticket: %v", err)
	}
	fetch.resp = successResp(order.ID, "mp-crash", "250.00", "MXN")
	rec := postMPWebhook(t, mux, "mp-crash", "order", "order", "mp-crash", "req-crash", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(sender.orders) != 0 {
		t.Fatalf("sends %d", len(sender.orders))
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Token.String != "already-issued" {
		t.Fatalf("tickets %+v", tickets)
	}
}

func TestPaymentTopicDoesNotFetch(t *testing.T) {
	fetch := &fakeFetcher{err: errors.New("should not fetch")}
	mux, _ := webhookMux(t, fetch, &fakeSender{}, openNow())
	rec := postMPWebhook(t, mux, "mp-pay", "payment", "payment", "mp-pay", "req-pay", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(fetch.calls) != 0 {
		t.Fatalf("fetches %v", fetch.calls)
	}
}

func TestUnknownReferenceOK(t *testing.T) {
	fetch := &fakeFetcher{resp: &mporder.Response{
		ID:                "mp-missing",
		ExternalReference: "999999",
		Status:            "processed",
		StatusDetail:      "accredited",
		TotalAmount:       "250.00",
		Currency:          "MXN",
	}}
	mux, _ := webhookMux(t, fetch, &fakeSender{}, openNow())
	rec := postMPWebhook(t, mux, "mp-missing", "order", "order", "mp-missing", "req-miss", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(fetch.calls) != 1 {
		t.Fatalf("fetches %v", fetch.calls)
	}
}

func TestBadSignatureDoesNotFetch(t *testing.T) {
	fetch := &fakeFetcher{resp: successResp(1, "mp-bad", "250.00", "MXN")}
	mux, _ := webhookMux(t, fetch, &fakeSender{}, openNow())
	rec := postMPWebhook(t, mux, "mp-bad", "order", "order", "mp-bad", "req-bad", "ts=1,v1=deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	if len(fetch.calls) != 0 {
		t.Fatalf("fetches %v", fetch.calls)
	}
}

func TestStalePendingTakesReinsertPath(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	p := pendingParams(now, 2, "stale-wh")
	p.ReservedUntil = now.UTC().Add(-time.Minute).Format(time.RFC3339)
	order := seedOrder(t, db, p, 2, "mp-stale")
	old, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("old tickets: %v", err)
	}
	if len(old) != 2 {
		t.Fatalf("old %d", len(old))
	}
	oldIDs := map[int64]bool{old[0].ID: true, old[1].ID: true}
	fetch.resp = successResp(order.ID, "mp-stale", "500.00", "MXN")

	rec := postMPWebhook(t, mux, "mp-stale", "order", "order", "mp-stale", "req-stale", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" || got.FulfillmentStatus != "fulfilled" {
		t.Fatalf("order %+v", got)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	assertIssued(t, tickets, 2)
	for _, tk := range tickets {
		if oldIDs[tk.ID] {
			t.Fatalf("issued reserved row %d", tk.ID)
		}
	}
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestPayGoDoesNotCallApply(t *testing.T) {
	body, err := os.ReadFile("pay.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "Apply(") {
		t.Fatal("pay.go calls Apply")
	}
}

func TestWebhookGetErrorIs500(t *testing.T) {
	fetch := &fakeFetcher{err: errors.New("mp down")}
	mux, _ := webhookMux(t, fetch, &fakeSender{}, openNow())
	rec := postMPWebhook(t, mux, "mp-down", "order", "order", "mp-down", "req-down", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRejectedFailureIsNoop(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	p := pendingParams(now, 1, "rej")
	p.Status = "rejected"
	order := seedOrder(t, db, p, 0, "mp-rej")
	fetch.resp = &mporder.Response{
		ID:                "mp-rej",
		ExternalReference: strconv.FormatInt(order.ID, 10),
		Status:            "canceled",
		StatusDetail:      "canceled",
	}
	rec := postMPWebhook(t, mux, "mp-rej", "order", "order", "mp-rej", "req-rej", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "rejected" {
		t.Fatalf("status %q", got.Status)
	}
	if len(sender.orders) != 0 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestExpiredFailureBecomesRejected(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	p := pendingParams(now, 1, "exp-fail")
	p.Status = "expired"
	order := seedOrder(t, db, p, 0, "mp-exp-fail")
	fetch.resp = &mporder.Response{
		ID:                "mp-exp-fail",
		ExternalReference: strconv.FormatInt(order.ID, 10),
		Status:            "expired",
		StatusDetail:      "expired",
	}
	rec := postMPWebhook(t, mux, "mp-exp-fail", "order", "order", "mp-exp-fail", "req-ef", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "rejected" {
		t.Fatalf("status %q", got.Status)
	}
	if len(sender.orders) != 0 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestResolveByMpOrderID(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 1, "by-mp"), 1, "mp-only")
	fetch.resp = &mporder.Response{
		ID:          "mp-only",
		Status:      "processed",
		StatusDetail: "accredited",
		TotalAmount: "250.00",
		Currency:    "MXN",
	}
	rec := postMPWebhook(t, mux, "mp-only", "order", "order", "mp-only", "req-mp", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" {
		t.Fatalf("status %q", got.Status)
	}
	if len(sender.orders) != 1 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestPendingFailureReleasesSeats(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 2, "wh-fail"), 2, "mp-wh-fail")
	fetch.resp = &mporder.Response{
		ID:                "mp-wh-fail",
		ExternalReference: strconv.FormatInt(order.ID, 10),
		Status:            "failed",
		StatusDetail:      "cc_rejected_3ds_challenge",
	}
	rec := postMPWebhook(t, mux, "mp-wh-fail", "order", "order", "mp-wh-fail", "req-wh-fail", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "rejected" {
		t.Fatalf("status %q", got.Status)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	if len(tickets) != 0 {
		t.Fatalf("tickets %d", len(tickets))
	}
	if len(sender.orders) != 0 {
		t.Fatalf("sends %d", len(sender.orders))
	}
}

func TestJSONDataIDUsedWhenQueryEmpty(t *testing.T) {
	now := openNow()
	fetch := &fakeFetcher{}
	sender := &fakeSender{}
	mux, db := webhookMux(t, fetch, sender, now)
	order := seedOrder(t, db, pendingParams(now, 1, "json-id"), 1, "MP-JSON")
	fetch.resp = successResp(order.ID, "mp-json", "250.00", "MXN")
	rec := postMPWebhook(t, mux, "", "order", "order", "MP-JSON", "req-json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if len(fetch.calls) != 1 || fetch.calls[0] != "mp-json" {
		t.Fatalf("fetches %v", fetch.calls)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "paid" {
		t.Fatalf("status %q", got.Status)
	}
}

func TestPayStillDoesNotIssueOnProcessed(t *testing.T) {
	charger := &fakeCharger{result: payment.Result{
		Kind:         payment.KindOrder,
		MPOrderID:    "mp-sync-still",
		Status:       "processed",
		StatusDetail: "accredited",
	}}
	mux, db := testMux(t, charger, openNow())
	postPay(t, mux, payValues("ck-sync-still"))
	order, err := db.Queries.GetOrderByCheckoutKey(context.Background(), "ck-sync-still")
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if order.Status != "pending_payment" {
		t.Fatalf("status %q", order.Status)
	}
	tickets, err := db.Queries.ListTicketsByOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	for _, tk := range tickets {
		if tk.Status != "reserved" || tk.Token.Valid {
			t.Fatalf("ticket %+v", tk)
		}
	}
}
