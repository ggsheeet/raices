package payment

import (
	"context"
	"fmt"
)

type Charge struct {
	LocalOrderID, IdempotencyKey, Token, PaymentMethodID string
	TotalCents                                           int
	GivenName, FamilyName, Email                         string
}

type Kind string

const (
	KindOrder   Kind = "order"
	KindNoOrder Kind = "no_order"
	KindUnknown Kind = "unknown"
)

type Result struct {
	Kind                                          Kind
	MPOrderID, Status, StatusDetail, ChallengeURL string
}

type Charger interface {
	Create(ctx context.Context, charge Charge) Result
}

func FormatCents(cents int) string {
	if cents < 0 {
		return fmt.Sprintf("-%d.%02d", (-cents)/100, (-cents)%100)
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
