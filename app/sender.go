package app

import (
	"context"

	"raices/database/sqlc"
)

type Sender interface {
	Send(ctx context.Context, order sqlc.Order) error
}

type noopSender struct{}

func (noopSender) Send(context.Context, sqlc.Order) error { return nil }
