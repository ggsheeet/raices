package app

import "time"

const (
	ticketCapacity  = 300
	holdDuration    = 15 * time.Minute
	pollGrace       = 2 * time.Minute
	emailStuckAfter = 10 * time.Minute
)

var salesCutoff = time.Date(2026, 10, 31, 6, 0, 0, 0, time.UTC)

func SalesOpen(now time.Time) bool {
	return now.UTC().Before(salesCutoff)
}

func sqliteNow(now time.Time) string {
	return now.UTC().Format("2006-01-02 15:04:05")
}

func reservedUntilStamp(now time.Time) string {
	return now.UTC().Add(holdDuration).Format(time.RFC3339)
}
