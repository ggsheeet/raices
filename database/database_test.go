package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
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

func TestOpenApplies000002ToExisting000001(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raices.db")
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	body, err := os.ReadFile("migrations/000001_init.sql")
	if err != nil {
		t.Fatalf("read 000001: %v", err)
	}
	if _, err := sqlDB.Exec(string(body)); err != nil {
		t.Fatalf("apply 000001: %v", err)
	}
	if _, err := sqlDB.Exec(`
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT DEFAULT (datetime('now'))
		);
		INSERT INTO schema_migrations (version) VALUES ('000001_init');
	`); err != nil {
		t.Fatalf("record 000001: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO orders (
			given_name, family_name, email, age, quantity,
			unit_price_cents, total_cents, status, fulfillment_status,
			email_status, reserved_until
		) VALUES (
			'Ada', 'Lovelace', 'ada@example.com', 36, 1,
			25000, 25000, 'pending_payment', 'pending',
			'not_sent', '2026-10-14T12:00:00Z'
		)
	`); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close seed: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	names := map[string]bool{}
	rows, err := db.sql.Query(`PRAGMA table_info(orders)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names[name] = true
	}
	if !names["poll_token"] || !names["checkout_key"] {
		t.Fatalf("columns = %v, want poll_token and checkout_key", names)
	}

	var poll, key string
	if err := db.sql.QueryRow(`SELECT poll_token, checkout_key FROM orders LIMIT 1`).Scan(&poll, &key); err != nil {
		t.Fatalf("select tokens: %v", err)
	}
	if poll != "" || key != "" {
		t.Fatalf("legacy row poll_token=%q checkout_key=%q, want empty defaults", poll, key)
	}
}
