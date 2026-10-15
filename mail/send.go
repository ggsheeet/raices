package mail

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/resend/resend-go/v2"

	"raices/database"
	"raices/database/sqlc"
	"raices/env"
	"raices/payment"
)

const (
	maxEmailErrorRunes = 500
	waitBeforeSecond   = 300 * time.Millisecond
	waitBeforeThird    = 900 * time.Millisecond
)

type poster interface {
	SendWithContext(ctx context.Context, params *resend.SendEmailRequest) (*resend.SendEmailResponse, error)
}

type Sender struct {
	db     *database.DB
	poster poster
	encode func(TicketCard) ([]byte, error)
	now    func() time.Time
	sleep  func(ctx context.Context, d time.Duration) error
}

func New(db *database.DB, apiKey string) *Sender {
	client := resend.NewClient(apiKey)
	return &Sender{
		db:     db,
		poster: client.Emails,
		encode: encodePNG,
		now:    time.Now,
		sleep:  wait,
	}
}

func encodePNG(card TicketCard) ([]byte, error) {
	return renderTicketPNG(card)
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *Sender) Send(ctx context.Context, order sqlc.Order) error {
	tickets, err := s.db.Queries.ListTicketsByOrder(ctx, order.ID)
	if err != nil {
		if persistErr := s.persist(ctx, order.ID, "failed", err.Error()); persistErr != nil {
			return persistErr
		}
		return err
	}

	name := strings.TrimSpace(order.GivenName + " " + order.FamilyName)
	count := int(order.Quantity)
	if count < 1 {
		count = len(tickets)
	}
	pngs := make([][]byte, 0, len(tickets))
	indexes := make([]int, 0, len(tickets))
	for i, tk := range tickets {
		if tk.Status != "issued" || !tk.Token.Valid || tk.Token.String == "" {
			continue
		}
		png, err := s.encode(TicketCard{
			URL:   claimURL(tk.Token.String),
			Name:  name,
			Index: i + 1,
			Count: count,
		})
		if err != nil {
			msg := "qr encode failed"
			if persistErr := s.persist(ctx, order.ID, "failed", msg); persistErr != nil {
				return persistErr
			}
			return err
		}
		pngs = append(pngs, png)
		indexes = append(indexes, i+1)
	}

	if len(pngs) == 0 {
		msg := "no issued tickets with tokens"
		if persistErr := s.persist(ctx, order.ID, "failed", msg); persistErr != nil {
			return persistErr
		}
		return fmt.Errorf("%s", msg)
	}

	req := &resend.SendEmailRequest{
		From:    env.ResendFrom,
		To:      []string{order.Email},
		Subject: "Tus boletos para Raíces 2026",
		Html:    buildHTML(order, indexes),
	}
	for i, png := range pngs {
		n := i + 1
		req.Attachments = append(req.Attachments, &resend.Attachment{
			Content:     png,
			Filename:    fmt.Sprintf("ticket-%d.png", n),
			ContentType: "image/png",
			ContentId:   fmt.Sprintf("ticket-%d", n),
		})
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt == 1 {
			if err := s.sleep(ctx, waitBeforeSecond); err != nil {
				_ = s.persist(ctx, order.ID, "failed", err.Error())
				return err
			}
		}
		if attempt == 2 {
			if err := s.sleep(ctx, waitBeforeThird); err != nil {
				_ = s.persist(ctx, order.ID, "failed", err.Error())
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			_ = s.persist(ctx, order.ID, "failed", err.Error())
			return err
		}
		_, err := s.poster.SendWithContext(ctx, req)
		if err == nil {
			return s.persist(ctx, order.ID, "sent", "")
		}
		lastErr = err
	}

	msg := lastErr.Error()
	if persistErr := s.persist(ctx, order.ID, "failed", msg); persistErr != nil {
		return persistErr
	}
	return lastErr
}

func (s *Sender) persist(ctx context.Context, orderID int64, status, lastErr string) error {
	var errCol sql.NullString
	if lastErr != "" {
		errCol = sql.NullString{String: truncateRunes(lastErr, maxEmailErrorRunes), Valid: true}
	}
	return s.db.Queries.UpdateOrderEmailStatus(ctx, sqlc.UpdateOrderEmailStatusParams{
		EmailStatus:    status,
		EmailLastError: errCol,
		UpdatedAt:      s.now().UTC().Format("2006-01-02 15:04:05"),
		ID:             orderID,
	})
}

func claimURL(token string) string {
	return env.AppBaseURL + "/tickets/claim/" + token
}

func buildHTML(order sqlc.Order, indexes []int) string {
	name := html.EscapeString(strings.TrimSpace(order.GivenName + " " + order.FamilyName))
	unit := html.EscapeString(payment.FormatCents(int(order.UnitPriceCents)))
	total := html.EscapeString(payment.FormatCents(int(order.TotalCents)))
	qty := order.Quantity
	noun := "boletos"
	if qty == 1 {
		noun = "boleto"
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>`)
	b.WriteString(`<body style="margin:0;padding:0;background:#E8E0CF;">`)
	fmt.Fprintf(&b, `<div style="display:none;max-height:0;overflow:hidden;">%s, tus boletos para %s ya están listos.</div>`, name, html.EscapeString(eventName))
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#E8E0CF;"><tr><td align="center" style="padding:32px 16px;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;background:#F6F1E6;border-radius:12px;overflow:hidden;">`)
	b.WriteString(`<tr><td style="background:#52645A;color:#F6F1E6;padding:28px 32px 22px;font-family:Georgia,'Times New Roman',serif;">`)
	fmt.Fprintf(&b, `<p style="margin:0;font-family:ui-sans-serif,system-ui,sans-serif;font-size:12px;letter-spacing:0.14em;text-transform:uppercase;">%s</p>`, html.EscapeString(eventPlace))
	fmt.Fprintf(&b, `<h1 style="margin:12px 0 0;font-size:34px;line-height:1.05;font-weight:700;">%s</h1>`, html.EscapeString(eventName))
	fmt.Fprintf(&b, `<p style="margin:10px 0 0;font-size:18px;line-height:1.3;">%s</p>`, html.EscapeString(eventSeries))
	fmt.Fprintf(&b, `<p style="margin:6px 0 0;font-family:ui-sans-serif,system-ui,sans-serif;font-size:14px;letter-spacing:0.04em;">%s</p>`, html.EscapeString(eventDates))
	b.WriteString(`</td></tr><tr><td style="height:8px;background:#D6B978;font-size:0;line-height:0;">&nbsp;</td></tr>`)
	b.WriteString(`<tr><td style="padding:28px 32px 32px;color:#292A26;font-family:ui-sans-serif,system-ui,-apple-system,'Segoe UI',sans-serif;font-size:16px;line-height:1.5;">`)
	fmt.Fprintf(&b, `<p style="margin:0 0 12px;">Hola %s,</p>`, name)
	fmt.Fprintf(&b, `<p style="margin:0 0 12px;">Tu lugar está confirmado. En este correo va %d %s, cada uno con su código QR.</p>`, qty, noun)
	fmt.Fprintf(&b, `<p style="margin:0 0 20px;">%s</p>`, html.EscapeString(doorNote))
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-top:1px solid #d9d0bc;">`)
	fmt.Fprintf(&b, `<tr><td style="padding:12px 0 4px;font-size:14px;">Admisión general</td><td align="right" style="padding:12px 0 4px;font-size:14px;">Mex$%s</td></tr>`, unit)
	fmt.Fprintf(&b, `<tr><td style="padding:4px 0 12px;font-size:14px;">Cantidad</td><td align="right" style="padding:4px 0 12px;font-size:14px;">%d</td></tr>`, qty)
	fmt.Fprintf(&b, `<tr><td style="padding:12px 0;border-top:1px solid #d9d0bc;font-size:16px;font-weight:700;">Total</td><td align="right" style="padding:12px 0;border-top:1px solid #d9d0bc;font-size:16px;font-weight:700;">Mex$%s</td></tr>`, total)
	b.WriteString(`</table>`)
	fmt.Fprintf(&b, `<p style="margin:16px 0 0;font-size:14px;"><strong>%s</strong><br>%s<br>%s</p>`, html.EscapeString(eventPlace), html.EscapeString(eventStreet), html.EscapeString(eventCity))
	for i, index := range indexes {
		n := i + 1
		fmt.Fprintf(&b, `<p style="margin:28px 0 8px;font-size:13px;font-weight:700;letter-spacing:0.08em;text-transform:uppercase;color:#52645A;">Boleto %d</p>`, index)
		fmt.Fprintf(&b, `<img src="cid:ticket-%d" alt="Boleto %d" width="320" style="display:block;width:100%%;max-width:320px;height:auto;border:1px solid #d9d0bc;border-radius:8px;">`, n, index)
	}
	b.WriteString(`<p style="margin:28px 0 0;font-size:13px;color:#52645A;">Guarda este correo. Si no ves las imágenes, abre los archivos adjuntos.</p>`)
	b.WriteString(`</td></tr></table></td></tr></table></body></html>`)
	return b.String()
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
