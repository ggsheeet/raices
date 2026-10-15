ALTER TABLE orders ADD COLUMN poll_token TEXT NOT NULL DEFAULT '';
ALTER TABLE orders ADD COLUMN checkout_key TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS orders_poll_token_uidx ON orders(poll_token) WHERE poll_token <> '';
CREATE UNIQUE INDEX IF NOT EXISTS orders_checkout_key_uidx ON orders(checkout_key) WHERE checkout_key <> '';
