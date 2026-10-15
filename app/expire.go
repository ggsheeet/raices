package app

import (
	"context"
	"time"

	"raices/database/sqlc"
)

func ExpireStale(ctx context.Context, q *sqlc.Queries, now time.Time) error {
	until := now.UTC().Format(time.RFC3339)
	if err := q.DeleteStaleReservedTickets(ctx, until); err != nil {
		return err
	}
	return q.ExpireStaleOrders(ctx, sqlc.ExpireStaleOrdersParams{
		UpdatedAt:     sqliteNow(now),
		ReservedUntil: until,
	})
}
