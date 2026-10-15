package app

import (
	"testing"
	"time"
)

func TestSalesOpenCutoff(t *testing.T) {
	openAt := time.Date(2026, 10, 31, 5, 59, 59, 0, time.UTC)
	closedAt := time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)
	if !SalesOpen(openAt) {
		t.Fatal("expected sales open at 05:59:59Z")
	}
	if SalesOpen(closedAt) {
		t.Fatal("expected sales closed at 06:00:00Z")
	}
}
