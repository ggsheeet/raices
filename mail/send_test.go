package mail

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/resend/resend-go/v2"
	qrcode "github.com/skip2/go-qrcode"

	"raices/database"
	"raices/database/sqlc"
	"raices/env"
	"raices/payment"
)

var pngMagic = []byte{0x89, 'P', 'N', 'G'}

type fakePoster struct {
	errs []error
	reqs []*resend.SendEmailRequest
}

func (f *fakePoster) SendWithContext(_ context.Context, params *resend.SendEmailRequest) (*resend.SendEmailResponse, error) {
	n := len(f.reqs)
	f.reqs = append(f.reqs, params)
	if n < len(f.errs) && f.errs[n] != nil {
		return nil, f.errs[n]
	}
	return &resend.SendEmailResponse{Id: "email-1"}, nil
}

func setMailEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SQLITE_PATH", filepath.Join(t.TempDir(), "unused.db"))
	t.Setenv("APP_BASE_URL", "http://tickets.example")
	t.Setenv("TICKET_PRICE", "250")
	t.Setenv("MP_ACCESS_TOKEN", "mp-access-token")
	t.Setenv("MP_PUBLIC_KEY", "mp-public-key")
	t.Setenv("MP_WEBHOOK_SECRET", "mp-webhook-secret")
	t.Setenv("RESEND_API_KEY", "resend-api-key")
	t.Setenv("RESEND_FROM", "boletos@example.com")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "admin-password")
	if err := env.Sync(); err != nil {
		t.Fatalf("env.Sync: %v", err)
	}
}

func openDB(t *testing.T) *database.DB {
	t.Helper()
	setMailEnv(t)
	db, err := database.Open(filepath.Join(t.TempDir(), "raices.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedPaidOrder(t *testing.T, db *database.DB, qty int, tokens []string) sqlc.Order {
	t.Helper()
	ctx := context.Background()
	unit := int64(33300)
	order, err := db.Queries.InsertOrder(ctx, sqlc.InsertOrderParams{
		GivenName:         "Ada",
		FamilyName:        "Lovelace",
		Email:             "ada@example.com",
		Age:               36,
		Quantity:          int64(qty),
		UnitPriceCents:    unit,
		TotalCents:        unit * int64(qty),
		Status:            "paid",
		FulfillmentStatus: "fulfilled",
		EmailStatus:       "not_sent",
		ReservedUntil:     time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339),
		PollToken:         "poll-" + t.Name(),
		CheckoutKey:       "ck-" + t.Name(),
	})
	if err != nil {
		t.Fatalf("InsertOrder: %v", err)
	}
	for i, tok := range tokens {
		p := sqlc.InsertTicketParams{OrderID: order.ID, Status: "issued"}
		if tok != "" {
			p.Token = sql.NullString{String: tok, Valid: true}
		}
		if _, err := db.Queries.InsertTicket(ctx, p); err != nil {
			t.Fatalf("ticket %d: %v", i, err)
		}
	}
	return order
}

func newTestSender(db *database.DB, poster *fakePoster) *Sender {
	return &Sender{
		db:     db,
		poster: poster,
		encode: func(card TicketCard) ([]byte, error) {
			return qrcode.Encode(card.URL, qrcode.Medium, 256)
		},
		now: time.Now,
		sleep: func(ctx context.Context, _ time.Duration) error {
			return ctx.Err()
		},
	}
}

func TestSendFirstAttemptSuccess(t *testing.T) {
	db := openDB(t)
	order := seedPaidOrder(t, db, 1, []string{"tok-one"})
	poster := &fakePoster{}
	sender := newTestSender(db, poster)

	if err := sender.Send(context.Background(), order); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(poster.reqs) != 1 {
		t.Fatalf("poster calls %d, want 1", len(poster.reqs))
	}
	req := poster.reqs[0]
	if req.From != env.ResendFrom {
		t.Fatalf("From %q", req.From)
	}
	if len(req.To) != 1 || req.To[0] != order.Email {
		t.Fatalf("To %#v", req.To)
	}
	total := payment.FormatCents(int(order.TotalCents))
	if !strings.Contains(req.Html, "Ada Lovelace") {
		t.Fatalf("html missing buyer name: %s", req.Html)
	}
	if !strings.Contains(req.Html, total) {
		t.Fatalf("html missing peso total %q: %s", total, req.Html)
	}
	if !strings.Contains(req.Html, "cid:ticket-1") {
		t.Fatalf("html missing cid: %s", req.Html)
	}
	if !strings.Contains(req.Html, "30 oct") || !strings.Contains(req.Html, "1 nov 2026") {
		t.Fatalf("html missing dates: %s", req.Html)
	}
	if !strings.Contains(req.Html, "personal de la puerta") {
		t.Fatalf("html missing door copy: %s", req.Html)
	}
	if !strings.Contains(req.Html, "INP Emanuel") || !strings.Contains(req.Html, "background:#52645A") {
		t.Fatalf("html missing styled event block: %s", req.Html)
	}
	if len(req.Attachments) != 1 {
		t.Fatalf("attachments %d", len(req.Attachments))
	}
	att := req.Attachments[0]
	if att.ContentId != "ticket-1" {
		t.Fatalf("ContentId %q", att.ContentId)
	}
	if !bytes.HasPrefix(att.Content, pngMagic) {
		t.Fatalf("attachment is not PNG")
	}

	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "sent" {
		t.Fatalf("email_status %q", got.EmailStatus)
	}
	if got.EmailLastError.Valid {
		t.Fatalf("email_last_error %q", got.EmailLastError.String)
	}
}

func TestSendTwoFailuresThenSuccess(t *testing.T) {
	db := openDB(t)
	order := seedPaidOrder(t, db, 1, []string{"tok-retry"})
	poster := &fakePoster{errs: []error{errors.New("fail-1"), errors.New("fail-2")}}
	sender := newTestSender(db, poster)
	var waits []time.Duration
	sender.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}

	if err := sender.Send(context.Background(), order); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(poster.reqs) != 3 {
		t.Fatalf("poster calls %d, want 3", len(poster.reqs))
	}
	if len(waits) != 2 || waits[0] != waitBeforeSecond || waits[1] != waitBeforeThird {
		t.Fatalf("waits %#v", waits)
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "sent" {
		t.Fatalf("email_status %q", got.EmailStatus)
	}
}

func TestSendThreeFailures(t *testing.T) {
	db := openDB(t)
	order := seedPaidOrder(t, db, 1, []string{"tok-fail"})
	poster := &fakePoster{errs: []error{
		errors.New("boom-1"),
		errors.New("boom-2"),
		errors.New("boom-final"),
	}}
	sender := newTestSender(db, poster)

	err := sender.Send(context.Background(), order)
	if err == nil {
		t.Fatal("expected error")
	}
	if len(poster.reqs) != 3 {
		t.Fatalf("poster calls %d, want 3", len(poster.reqs))
	}

	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "failed" {
		t.Fatalf("email_status %q", got.EmailStatus)
	}
	if !got.EmailLastError.Valid || got.EmailLastError.String != "boom-final" {
		t.Fatalf("email_last_error %#v", got.EmailLastError)
	}
}

func TestSendQRPayload(t *testing.T) {
	db := openDB(t)
	token := "claim-token-xyz"
	order := seedPaidOrder(t, db, 2, []string{token, ""})
	poster := &fakePoster{}
	var payloads []string
	sender := newTestSender(db, poster)
	sender.encode = func(card TicketCard) ([]byte, error) {
		payloads = append(payloads, card.URL)
		return qrcode.Encode(card.URL, qrcode.Medium, 256)
	}

	if err := sender.Send(context.Background(), order); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := env.AppBaseURL + "/tickets/claim/" + token
	if len(payloads) != 1 || payloads[0] != want {
		t.Fatalf("payloads %#v want %q", payloads, want)
	}
	if len(poster.reqs) != 1 || len(poster.reqs[0].Attachments) != 1 {
		t.Fatalf("expected one attachment for one issued token")
	}
	if !bytes.HasPrefix(poster.reqs[0].Attachments[0].Content, pngMagic) {
		t.Fatalf("attachment is not PNG")
	}
}

func TestSendNoIssuedTokensSkipsResend(t *testing.T) {
	db := openDB(t)
	order := seedPaidOrder(t, db, 1, []string{""})
	poster := &fakePoster{}
	sender := newTestSender(db, poster)

	if err := sender.Send(context.Background(), order); err == nil {
		t.Fatal("expected error")
	}
	if len(poster.reqs) != 0 {
		t.Fatalf("poster calls %d, want 0", len(poster.reqs))
	}
	got, err := db.Queries.GetOrderByID(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("GetOrderByID: %v", err)
	}
	if got.EmailStatus != "failed" || !got.EmailLastError.Valid {
		t.Fatalf("status %+v", got)
	}
}
