package env

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

var (
	Port                 string
	Environment          string
	AppBaseURL           string
	SqlitePath           string
	TicketPriceCents     int
	MPPublicKey          string
	MPAccessToken        string
	MPWebhookSecret      string
	ResendAPIKey         string
	ResendFrom           string
	AdminEmail           string
	AdminPassword        string
	LitestreamReplicaURL string
)

func init() {
	_ = Sync()
}

func Sync() error {
	Port = getenv("PORT", "3000")
	Environment = getenv("ENVIRONMENT", "development")
	LitestreamReplicaURL = strings.TrimSpace(os.Getenv("LITESTREAM_REPLICA_URL"))

	var err error
	if SqlitePath, err = required("SQLITE_PATH"); err != nil {
		return err
	}
	if AppBaseURL, err = required("APP_BASE_URL"); err != nil {
		return err
	}
	if MPAccessToken, err = required("MP_ACCESS_TOKEN"); err != nil {
		return err
	}
	if MPPublicKey, err = required("MP_PUBLIC_KEY"); err != nil {
		return err
	}
	if MPWebhookSecret, err = required("MP_WEBHOOK_SECRET"); err != nil {
		return err
	}
	if ResendAPIKey, err = required("RESEND_API_KEY"); err != nil {
		return err
	}
	if ResendFrom, err = required("RESEND_FROM"); err != nil {
		return err
	}
	if AdminEmail, err = required("ADMIN_EMAIL"); err != nil {
		return err
	}
	if AdminPassword, err = required("ADMIN_PASSWORD"); err != nil {
		return err
	}
	if TicketPriceCents, err = parseTicketPrice(); err != nil {
		return err
	}
	return nil
}

func required(key string) (string, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func parseTicketPrice() (int, error) {
	raw := strings.TrimSpace(os.Getenv("TICKET_PRICE"))
	if raw == "" {
		return 0, fmt.Errorf("TICKET_PRICE is required")
	}
	pesos, err := strconv.Atoi(raw)
	if err != nil || pesos <= 0 || pesos > math.MaxInt/100 {
		return 0, fmt.Errorf("TICKET_PRICE must be a positive whole number of pesos")
	}
	return pesos * 100, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
