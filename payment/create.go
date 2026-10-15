package payment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mercadopago/sdk-go/pkg/mperror"
	"github.com/mercadopago/sdk-go/pkg/order"
	"github.com/mercadopago/sdk-go/pkg/requestoptions"
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
		return Result{Kind: KindUnknown}
	}

	amount := FormatCents(charge.TotalCents)
	method := &order.PaymentMethodRequest{
		Type:         "credit_card",
		Token:        charge.Token,
		Installments: 1,
	}
	if charge.PaymentMethodID != "" {
		method.ID = charge.PaymentMethodID
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
		Config: &order.ConfigRequest{
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

	var respErr *mperror.ResponseError
	if errors.As(err, &respErr) && respErr != nil {
		if respErr.StatusCode == 409 || respErr.StatusCode == 429 || respErr.StatusCode >= 500 {
			return Result{Kind: KindUnknown}
		}
		var parsed order.Response
		if json.Unmarshal([]byte(respErr.Message), &parsed) == nil && (parsed.ID != "" || parsed.Status != "") {
			return resultFromOrder(&parsed)
		}
		if respErr.StatusCode == 400 || respErr.StatusCode == 422 {
			return Result{Kind: KindNoOrder}
		}
		return Result{Kind: KindUnknown}
	}

	return Result{Kind: KindUnknown}
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
