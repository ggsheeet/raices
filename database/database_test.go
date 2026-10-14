package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raices.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestOpenRequiresPath(t *testing.T) {
	_, err := Open("")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SQLITE_PATH") {
		t.Fatalf("error %q does not contain SQLITE_PATH", err)
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	db := testDB(t)
	_, err := db.sql.Exec(`INSERT INTO tickets (order_id, token, status) VALUES (99999, NULL, 'reserved')`)
	if err == nil {
		t.Fatal("expected foreign key error")
	}
}

func TestHeldCountIncludesReservedIssuedAndUsed(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	res, err := db.sql.Exec(`
		INSERT INTO orders (
			given_name, family_name, email, age, quantity,
			unit_price_cents, total_cents, status, fulfillment_status,
			email_status, reserved_until
		) VALUES (
			'Ada', 'Lovelace', 'ada@example.com', 36, 3,
			25000, 75000, 'pending_payment', 'pending',
			'not_sent', datetime('now', '+15 minutes')
		)
	`)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("order id: %v", err)
	}

	if _, err := db.sql.Exec(
		`INSERT INTO tickets (order_id, token, status) VALUES (?, NULL, 'reserved')`,
		orderID,
	); err != nil {
		t.Fatalf("insert reserved ticket: %v", err)
	}
	if _, err := db.sql.Exec(
		`INSERT INTO tickets (order_id, token, status) VALUES (?, 'issued-token', 'issued')`,
		orderID,
	); err != nil {
		t.Fatalf("insert issued ticket: %v", err)
	}
	if _, err := db.sql.Exec(
		`INSERT INTO tickets (order_id, token, status) VALUES (?, 'used-token', 'used')`,
		orderID,
	); err != nil {
		t.Fatalf("insert used ticket: %v", err)
	}

	count, err := db.Queries.CountTickets(ctx)
	if err != nil {
		t.Fatalf("CountTickets: %v", err)
	}
	if count != 3 {
		t.Fatalf("CountTickets = %d, want 3", count)
	}
}
