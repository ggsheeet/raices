package payment

import (
	"raices/env"

	"github.com/mercadopago/sdk-go/pkg/config"
	"github.com/mercadopago/sdk-go/pkg/order"
)

type MPClient struct {
	order.Client
}

func NewMPClient() (*MPClient, error) {
	c, err := config.New(env.MPAccessToken)
	if err != nil {
		return nil, err
	}

	client := order.NewClient(c)
	return &MPClient{Client: client}, nil
}
