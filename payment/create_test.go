package payment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mercadopago/sdk-go/pkg/mperror"
	"github.com/mercadopago/sdk-go/pkg/order"
	"github.com/mercadopago/sdk-go/pkg/requestoptions"
)

type fakeOrderClient struct {
	order.Client
	err  error
	resp *order.Response
	key  string
	req  order.Request
}

func (f *fakeOrderClient) Create(ctx context.Context, req order.Request) (*order.Response, error) {
	f.req = req
	f.key, _ = requestoptions.IdempotencyKeyFrom(ctx)
	return f.resp, f.err
}

func TestCreateSendsQualityFields(t *testing.T) {
	fake := &fakeOrderClient{resp: &order.Response{ID: "mp-1", Status: "processed"}}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{
		TotalCents:   50000,
		UnitCents:    25000,
		Quantity:     2,
		Token:        "tok",
		RegisteredAt: "2026-10-19 16:38:00",
	})
	if got.Kind != KindOrder {
		t.Fatalf("result = %+v", got)
	}
	if len(fake.req.Items) != 1 {
		t.Fatalf("items = %+v", fake.req.Items)
	}
	item := fake.req.Items[0]
	if item.Title != itemTitle || item.ExternalCode != itemCode || item.CategoryID != itemCategory || item.UnitPrice != "250.00" || item.Quantity != 2 {
		t.Fatalf("item = %+v", item)
	}
	if fake.req.AdditionalInfo == nil || fake.req.AdditionalInfo.PayerRegistrationDate != "2026-10-19T16:38:00.000+00:00" {
		t.Fatalf("additional_info = %+v", fake.req.AdditionalInfo)
	}
	if fake.req.TotalAmount != "500.00" {
		t.Fatalf("total = %q", fake.req.TotalAmount)
	}
	if fake.req.Config == nil || fake.req.Config.StatementDescriptor != statementDescriptor {
		t.Fatalf("config = %+v", fake.req.Config)
	}
	payments := fake.req.Transactions.Payments
	if len(payments) != 1 || payments[0].PaymentMethod == nil || payments[0].PaymentMethod.StatementDescriptor != statementDescriptor {
		t.Fatalf("payment = %+v", payments)
	}

	body, err := json.Marshal(fake.req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	raw := string(body)
	for _, want := range []string{
		`"title":"Boleto Raíces 2026"`,
		`"external_code":"raices-2026"`,
		`"category_id":"tickets"`,
		`"unit_price":"250.00"`,
		`"quantity":2`,
		`"statement_descriptor":"RAICES"`,
		`"payer.registration_date":"2026-10-19T16:38:00.000+00:00"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("body missing %s: %s", want, raw)
		}
	}
}

func TestCreateNormalizesPaymentMethod(t *testing.T) {
	cases := []struct {
		id, kind, wantID, wantType string
	}{
		{id: "debvisa", kind: "debit_card", wantID: "visa", wantType: "debit_card"},
		{id: "debmaster", wantID: "master", wantType: "debit_card"},
		{id: "maestro", wantID: "master", wantType: "debit_card"},
		{id: "visa", wantID: "visa", wantType: "credit_card"},
		{id: "Visa", kind: "debit_card", wantID: "visa", wantType: "debit_card"},
		{id: "amex", kind: "credit_card", wantID: "amex", wantType: "credit_card"},
		{id: "master", kind: "prepaid_card", wantID: "master", wantType: "credit_card"},
	}
	for _, tc := range cases {
		fake := &fakeOrderClient{resp: &order.Response{ID: "mp-1", Status: "processed"}}
		got := orderCharger{client: fake}.Create(context.Background(), Charge{
			TotalCents:      25000,
			PaymentMethodID: tc.id,
			PaymentType:     tc.kind,
		})
		if got.Kind != KindOrder || got.Err != nil {
			t.Fatalf("%s/%s: result = %+v", tc.id, tc.kind, got)
		}
		pm := fake.req.Transactions.Payments[0].PaymentMethod
		if pm == nil || pm.ID != tc.wantID || pm.Type != tc.wantType || pm.Installments != 1 {
			t.Fatalf("%s/%s: method = %+v", tc.id, tc.kind, pm)
		}
	}
}

func TestCreateSendsMemberItem(t *testing.T) {
	fake := &fakeOrderClient{resp: &order.Response{ID: "mp-1", Status: "processed"}}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{
		TotalCents: 10000,
		UnitCents:  10000,
		Quantity:   1,
		Member:     true,
	})
	if got.Kind != KindOrder {
		t.Fatalf("result = %+v", got)
	}
	if len(fake.req.Items) != 1 {
		t.Fatalf("items = %+v", fake.req.Items)
	}
	item := fake.req.Items[0]
	if item.Title != memberItemTitle || item.ExternalCode != memberItemCode || item.UnitPrice != "100.00" {
		t.Fatalf("item = %+v", item)
	}
}

func TestRegistrationDateSkipsBlank(t *testing.T) {
	if registrationDate("") != "" || registrationDate("not-a-date") != "" {
		t.Fatal("expected empty registration date")
	}
}

func TestCreateMapsProcessedBody(t *testing.T) {
	fake := &fakeOrderClient{resp: &order.Response{ID: "mp-1", Status: "processed", StatusDetail: "accredited"}}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{IdempotencyKey: "9", TotalCents: 25000})
	if got.Kind != KindOrder || got.MPOrderID != "mp-1" || got.Status != "processed" {
		t.Fatalf("result = %+v", got)
	}
	if fake.key != "9" {
		t.Fatalf("idempotency key = %q", fake.key)
	}
}

func TestCreateMaps402OrderBody(t *testing.T) {
	body := `{"id":"mp-fail","status":"failed","status_detail":"failed"}`
	fake := &fakeOrderClient{err: &mperror.PaymentError{}}
	// PaymentError embeds typedBase which wraps *ResponseError; construct via BuildError.
	fake.err = mperror.BuildError(&mperror.ResponseError{StatusCode: 402, Message: body}, 0)
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindOrder || got.MPOrderID != "mp-fail" || got.Status != "failed" || got.Err == nil {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMaps400AsNoOrder(t *testing.T) {
	fake := &fakeOrderClient{err: mperror.BuildError(&mperror.ResponseError{StatusCode: 400, Message: "bad token"}, 0)}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindNoOrder || got.Err == nil {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMapsTimeoutUnknown(t *testing.T) {
	fake := &fakeOrderClient{err: context.DeadlineExceeded}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown || !errors.Is(got.Err, context.DeadlineExceeded) {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMaps409Unknown(t *testing.T) {
	fake := &fakeOrderClient{err: mperror.BuildError(&mperror.ResponseError{StatusCode: 409, Message: `{"id":"x"}`}, 0)}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown || got.MPOrderID != "" || got.Err == nil {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMapsTransportUnknown(t *testing.T) {
	fake := &fakeOrderClient{err: errors.New("transport level error")}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown || got.Err == nil {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateNilClient(t *testing.T) {
	got := orderCharger{}.Create(context.Background(), Charge{})
	if got.Kind != KindUnknown || !errors.Is(got.Err, errNoOrderClient) {
		t.Fatalf("result = %+v", got)
	}
}
