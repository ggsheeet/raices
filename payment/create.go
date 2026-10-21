package payment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mercadopago/sdk-go/pkg/mperror"
	"github.com/mercadopago/sdk-go/pkg/order"
	"github.com/mercadopago/sdk-go/pkg/requestoptions"
)

const (
	// statementDescriptor is what the buyer sees on the card statement.
	// Mercado Pago allows up to 13 letters or numbers.
	statementDescriptor = "RAICES"
	itemTitle           = "Boleto Raíces 2026"
	itemCode            = "raices-2026"
	memberItemTitle     = "Boleto Raíces 2026 Miembros Emanuel"
	memberItemCode      = "raices-2026-miembros"
	itemCategory        = "tickets"
)

type orderCharger struct {
	client order.Client
}

func NewCharger(mp *MPClient) Charger {
	if mp == nil {
		return orderCharger{}
	}
	return orderCharger{client: mp.Client}
}

func (c orderCharger) Create(ctx context.Context, charge Charge) Result {
	if c.client == nil {
		return Result{Kind: KindUnknown, Err: errNoOrderClient}
	}

	amount := FormatCents(charge.TotalCents)
	brand, kind := ordersPaymentMethod(charge.PaymentMethodID, charge.PaymentType)
	method := &order.PaymentMethodRequest{
		Type:                kind,
		Token:               charge.Token,
		Installments:        1,
		StatementDescriptor: statementDescriptor,
	}
	if brand != "" {
		method.ID = brand
	}

	req := order.Request{
		Type:              "online",
		CaptureMode:       "automatic",
		ProcessingMode:    "automatic",
		TotalAmount:       amount,
		ExternalReference: charge.LocalOrderID,
		Transactions: &order.TransactionRequest{
			Payments: []order.PaymentRequest{{
				Amount:        amount,
				PaymentMethod: method,
			}},
		},
		Payer: &order.PayerRequest{
			Email:     charge.Email,
			FirstName: charge.GivenName,
			LastName:  charge.FamilyName,
		},
		Items:          lineItems(charge),
		AdditionalInfo: payerInfo(charge),
		Config: &order.ConfigRequest{
			StatementDescriptor: statementDescriptor,
			Online: &order.OnlineConfigRequest{
				TransactionSecurity: &order.TransactionSecurityRequest{
					Validation:     "on_fraud_risk",
					LiabilityShift: "required",
				},
			},
		},
	}

	ctx = requestoptions.WithIdempotencyKey(ctx, charge.IdempotencyKey)
	resp, err := c.client.Create(ctx, req)
	if err == nil && resp != nil {
		return resultFromOrder(resp)
	}
	if err == nil {
		return Result{Kind: KindUnknown, Err: errors.New("mercadopago order response was empty")}
	}

	var respErr *mperror.ResponseError
	if errors.As(err, &respErr) && respErr != nil {
		if respErr.StatusCode == 409 || respErr.StatusCode == 429 || respErr.StatusCode >= 500 {
			return Result{Kind: KindUnknown, Err: err}
		}
		var parsed order.Response
		if json.Unmarshal([]byte(respErr.Message), &parsed) == nil && (parsed.ID != "" || parsed.Status != "") {
			out := resultFromOrder(&parsed)
			out.Err = err
			return out
		}
		if respErr.StatusCode == 400 || respErr.StatusCode == 422 {
			return Result{Kind: KindNoOrder, Err: err}
		}
		return Result{Kind: KindUnknown, Err: err}
	}

	return Result{Kind: KindUnknown, Err: err}
}

// ordersPaymentMethod pairs the BIN-lookup id with the card type Orders expects.
// Credit accepts visa, master, and amex. Debit accepts debvisa and debmaster.
// Sending a debit id as credit, or a credit id as debit, is rejected.
func ordersPaymentMethod(rawID, rawType string) (string, string) {
	id := strings.ToLower(strings.TrimSpace(rawID))
	kind := strings.ToLower(strings.TrimSpace(rawType))
	switch id {
	case "debvisa", "debmaster":
		kind = "debit_card"
	case "maestro":
		id = "debmaster"
		kind = "debit_card"
	}
	if kind == "debit_card" {
		switch id {
		case "visa":
			id = "debvisa"
		case "master":
			id = "debmaster"
		}
		return id, kind
	}
	// Prepaid Visa and Mastercard are charged as credit cards.
	return id, "credit_card"
}

func lineItems(charge Charge) []order.ItemsRequest {
	if charge.Quantity < 1 {
		return nil
	}
	title, code := itemTitle, itemCode
	if charge.Member {
		title, code = memberItemTitle, memberItemCode
	}
	return []order.ItemsRequest{{
		Title:        title,
		ExternalCode: code,
		CategoryID:   itemCategory,
		UnitPrice:    FormatCents(charge.UnitCents),
		Quantity:     charge.Quantity,
	}}
}

func payerInfo(charge Charge) *order.AdditionalInfoRequest {
	stamp := registrationDate(charge.RegisteredAt)
	if stamp == "" {
		return nil
	}
	return &order.AdditionalInfoRequest{PayerRegistrationDate: stamp}
}

func registrationDate(raw string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(raw), time.UTC)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02T15:04:05.000-07:00")
}

func resultFromOrder(resp *order.Response) Result {
	out := Result{
		Kind:         KindOrder,
		MPOrderID:    resp.ID,
		Status:       resp.Status,
		StatusDetail: resp.StatusDetail,
	}
	for _, p := range resp.Transactions.Payments {
		if p.PaymentMethod.TransactionSecurity != nil && p.PaymentMethod.TransactionSecurity.URL != "" {
			out.ChallengeURL = p.PaymentMethod.TransactionSecurity.URL
			break
		}
	}
	return out
}
