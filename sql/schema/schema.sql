CREATE TABLE orders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  given_name TEXT NOT NULL,
  family_name TEXT NOT NULL,
  email TEXT NOT NULL,
  age INTEGER NOT NULL,
  church TEXT,
  quantity INTEGER NOT NULL,
  unit_price_cents INTEGER NOT NULL,
  total_cents INTEGER NOT NULL,
  status TEXT NOT NULL,
  fulfillment_status TEXT NOT NULL,
  email_status TEXT NOT NULL,
  email_last_error TEXT,
  mp_order_id TEXT,
  reserved_until TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  poll_token TEXT NOT NULL,
  checkout_key TEXT NOT NULL,
  CHECK (status IN ('pending_payment', 'paid', 'rejected', 'expired')),
  CHECK (fulfillment_status IN ('pending', 'fulfilled', 'unfulfilled')),
  CHECK (email_status IN ('not_sent', 'sent', 'failed')),
  CHECK (quantity >= 1 AND quantity <= 10)
);

CREATE TABLE tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id INTEGER NOT NULL REFERENCES orders(id),
  token TEXT UNIQUE,
  status TEXT NOT NULL,
  used_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  CHECK (status IN ('reserved', 'issued', 'used'))
);

CREATE UNIQUE INDEX orders_poll_token_uidx ON orders(poll_token) WHERE poll_token <> '';
CREATE UNIQUE INDEX orders_checkout_key_uidx ON orders(checkout_key) WHERE checkout_key <> '';

CREATE INDEX tickets_order_id_idx ON tickets(order_id);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at TEXT NOT NULL
);
