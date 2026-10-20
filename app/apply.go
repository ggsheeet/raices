package app

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"raices/database/sqlc"
)

func Apply(ctx context.Context, q *sqlc.Queries, now time.Time, order sqlc.Order, success bool, totalAmount, currency string) (bool, error) {
	if order.Status == "paid" {
		if !success {
			logger.Warning("webhook failure for paid order %d", order.ID)
		}
		return false, nil
	}

	if success {
		return applySuccess(ctx, q, now, order, totalAmount, currency)
	}
	return false, applyFailure(ctx, q, now, order)
}

func applySuccess(ctx context.Context, q *sqlc.Queries, now time.Time, order sqlc.Order, totalAmount, currency string) (bool, error) {
	reserved, err := q.ListReservedTicketsByOrder(ctx, order.ID)
	if err != nil {
		return false, fmt.Errorf("list reserved tickets: %w", err)
	}
	if !amountMatches(totalAmount, currency, order.TotalCents) {
		logger.Error("apply order %d: payment amount or currency does not match", order.ID)
		if err := setPaid(ctx, q, now, order.ID, "unfulfilled"); err != nil {
			return false, fmt.Errorf("mark paid: %w", err)
		}
		return false, nil
	}
	if len(reserved) > 0 {
		if err := issueReserved(ctx, q, now, reserved); err != nil {
			return false, err
		}
		if err := setPaid(ctx, q, now, order.ID, "fulfilled"); err != nil {
			return false, fmt.Errorf("mark paid: %w", err)
		}
		return true, nil
	}

	if order.Status != "expired" && order.Status != "rejected" && order.Status != "pending_payment" {
		return false, nil
	}

	held, err := q.CountTickets(ctx)
	if err != nil {
		return false, fmt.Errorf("count tickets: %w", err)
	}
	if remainingSeats(held) < int(order.Quantity) {
		logger.Error("apply order %d: not enough seats to fulfill", order.ID)
		if err := setPaid(ctx, q, now, order.ID, "unfulfilled"); err != nil {
			return false, fmt.Errorf("mark paid: %w", err)
		}
		return false, nil
	}
	for i := int64(0); i < order.Quantity; i++ {
		token, err := newSecret()
		if err != nil {
			return false, err
		}
		if _, err := q.InsertTicket(ctx, sqlc.InsertTicketParams{
			OrderID: order.ID,
			Token:   sql.NullString{String: token, Valid: true},
			Status:  "issued",
		}); err != nil {
			return false, fmt.Errorf("insert ticket: %w", err)
		}
	}
	if err := setPaid(ctx, q, now, order.ID, "fulfilled"); err != nil {
		return false, fmt.Errorf("mark paid: %w", err)
	}
	return true, nil
}

func applyFailure(ctx context.Context, q *sqlc.Queries, now time.Time, order sqlc.Order) error {
	switch order.Status {
	case "pending_payment":
		if err := q.DeleteReservedTicketsByOrder(ctx, order.ID); err != nil {
			return fmt.Errorf("delete reserved tickets: %w", err)
		}
		if err := q.RejectPendingOrder(ctx, sqlc.RejectPendingOrderParams{
			UpdatedAt: sqliteNow(now),
			ID:        order.ID,
		}); err != nil {
			return fmt.Errorf("reject pending order: %w", err)
		}
		return nil
	case "expired":
		if err := q.RejectExpiredOrder(ctx, sqlc.RejectExpiredOrderParams{
			UpdatedAt: sqliteNow(now),
			ID:        order.ID,
		}); err != nil {
			return fmt.Errorf("reject expired order: %w", err)
		}
		return nil
	default:
		return nil
	}
}

func issueReserved(ctx context.Context, q *sqlc.Queries, now time.Time, tickets []sqlc.Ticket) error {
	stamp := sqliteNow(now)
	for _, tk := range tickets {
		token, err := newSecret()
		if err != nil {
			return fmt.Errorf("ticket token: %w", err)
		}
		if err := q.IssueReservedTicket(ctx, sqlc.IssueReservedTicketParams{
			Token:     sql.NullString{String: token, Valid: true},
			UpdatedAt: stamp,
			ID:        tk.ID,
		}); err != nil {
			return fmt.Errorf("issue reserved ticket: %w", err)
		}
	}
	return nil
}

func setPaid(ctx context.Context, q *sqlc.Queries, now time.Time, id int64, fulfillment string) error {
	return q.UpdateOrderPaid(ctx, sqlc.UpdateOrderPaidParams{
		FulfillmentStatus: fulfillment,
		UpdatedAt:         sqliteNow(now),
		ID:                id,
	})
}

func amountMatches(totalAmount, currency string, totalCents int64) bool {
	if !strings.EqualFold(strings.TrimSpace(currency), "MXN") {
		return false
	}
	cents, ok := parseAmountCents(totalAmount)
	return ok && cents == totalCents
}

func parseAmountCents(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	whole, frac, ok := strings.Cut(raw, ".")
	if !ok || len(frac) != 2 {
		return 0, false
	}
	if whole == "" || !digitsOnly(whole) || !digitsOnly(frac) {
		return 0, false
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, false
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	if w > (1<<63-1)/100 {
		return 0, false
	}
	return w*100 + f, true
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
