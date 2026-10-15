package payment

import (
	"context"
	"errors"

	"github.com/mercadopago/sdk-go/pkg/order"
)

var errNoOrderClient = errors.New("mercadopago order client is not configured")

type Fetcher interface {
	Get(ctx context.Context, orderID string) (*order.Response, error)
}

type orderFetcher struct {
	client order.Client
}

func NewFetcher(mp *MPClient) Fetcher {
	if mp == nil {
		return orderFetcher{}
	}
	return orderFetcher{client: mp.Client}
}

func (f orderFetcher) Get(ctx context.Context, orderID string) (*order.Response, error) {
	if f.client == nil {
		return nil, errNoOrderClient
	}
	return f.client.Get(ctx, orderID)
}
