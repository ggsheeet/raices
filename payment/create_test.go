package payment

import (
	"context"
	"errors"
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
}

func (f *fakeOrderClient) Create(ctx context.Context, _ order.Request) (*order.Response, error) {
	f.key, _ = requestoptions.IdempotencyKeyFrom(ctx)
	return f.resp, f.err
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
	if got.Kind != KindOrder || got.MPOrderID != "mp-fail" || got.Status != "failed" {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMaps400AsNoOrder(t *testing.T) {
	fake := &fakeOrderClient{err: mperror.BuildError(&mperror.ResponseError{StatusCode: 400, Message: "bad token"}, 0)}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindNoOrder {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestCreateMapsTimeoutUnknown(t *testing.T) {
	fake := &fakeOrderClient{err: context.DeadlineExceeded}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestCreateMaps409Unknown(t *testing.T) {
	fake := &fakeOrderClient{err: mperror.BuildError(&mperror.ResponseError{StatusCode: 409, Message: `{"id":"x"}`}, 0)}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown || got.MPOrderID != "" {
		t.Fatalf("result = %+v", got)
	}
}

func TestCreateMapsTransportUnknown(t *testing.T) {
	fake := &fakeOrderClient{err: errors.New("transport level error")}
	got := orderCharger{client: fake}.Create(context.Background(), Charge{TotalCents: 25000})
	if got.Kind != KindUnknown {
		t.Fatalf("kind = %q", got.Kind)
	}
}
