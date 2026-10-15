package payment

import "testing"

func TestFormatCents(t *testing.T) {
	if got := FormatCents(25000); got != "250.00" {
		t.Fatalf("FormatCents(25000) = %q, want 250.00", got)
	}
}
