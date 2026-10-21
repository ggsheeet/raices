package payment

import (
	"context"
	"fmt"
)

type Charge struct {
	LocalOrderID, IdempotencyKey, Token string
	// PaymentMethodID and PaymentType come from the card BIN lookup.
	// Credit ids are visa, master, and amex. Debit ids are debvisa and debmaster.
	PaymentMethodID, PaymentType    string
	TotalCents, UnitCents, Quantity int
	Member                          bool
	GivenName, FamilyName, Email    string
	// RegisteredAt is when this buyer first appeared on the site.
	// Checkout has no accounts, so that moment is the order row.
	RegisteredAt string
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
	// Err is the provider or transport failure behind this result.
	// Handlers log it; Kind still decides how checkout proceeds.
	Err error
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
