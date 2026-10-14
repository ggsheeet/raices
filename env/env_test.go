package env

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SQLITE_PATH", "./data/raices.db")
	t.Setenv("APP_BASE_URL", "http://localhost:3000")
	t.Setenv("TICKET_PRICE", "250")
	t.Setenv("MP_ACCESS_TOKEN", "mp-access-token")
	t.Setenv("MP_PUBLIC_KEY", "mp-public-key")
	t.Setenv("MP_WEBHOOK_SECRET", "mp-webhook-secret")
	t.Setenv("RESEND_API_KEY", "resend-api-key")
	t.Setenv("RESEND_FROM", "from@example.com")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "admin-password")
}

func TestSyncRejectsEmptyAdminPassword(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ADMIN_PASSWORD", "")
	err := Sync()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ADMIN_PASSWORD") {
		t.Fatalf("error %q does not name ADMIN_PASSWORD", err)
	}
}

func TestSyncRejectsNonIntegerPrice(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TICKET_PRICE", "250.00")
	err := Sync()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "TICKET_PRICE") {
		t.Fatalf("error %q does not name TICKET_PRICE", err)
	}
}

func TestSyncRejectsNonPositivePrice(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TICKET_PRICE", "0")
	err := Sync()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "TICKET_PRICE") {
		t.Fatalf("error %q does not name TICKET_PRICE", err)
	}
}

func TestSyncParsesPesosToCents(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TICKET_PRICE", "250")
	if err := Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if TicketPriceCents != 25000 {
		t.Fatalf("TicketPriceCents = %d, want 25000", TicketPriceCents)
	}
}
