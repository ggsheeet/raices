-- name: CountOrders :one
SELECT COUNT(*) FROM orders;

-- name: ListOrders :many
SELECT * FROM orders ORDER BY id;

-- name: GetOrderByID :one
SELECT * FROM orders WHERE id = ?;

-- name: GetOrderByCheckoutKey :one
SELECT * FROM orders WHERE checkout_key = ? AND checkout_key <> '';

-- name: GetOrderByPollToken :one
SELECT * FROM orders WHERE poll_token = ? AND poll_token <> '';

-- name: GetOrderByMpOrderID :one
SELECT * FROM orders WHERE mp_order_id = ? AND mp_order_id IS NOT NULL AND mp_order_id <> '';

-- name: InsertOrder :one
INSERT INTO orders (
  given_name,
  family_name,
  email,
  age,
  church,
  quantity,
  unit_price_cents,
  total_cents,
  status,
  fulfillment_status,
  email_status,
  reserved_until,
  poll_token,
  checkout_key
) VALUES (
  ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
) RETURNING *;

-- name: DeleteStaleReservedTickets :exec
DELETE FROM tickets
WHERE status = 'reserved'
  AND order_id IN (
    SELECT id FROM orders
    WHERE status = 'pending_payment' AND reserved_until < ?
  );

-- name: ExpireStaleOrders :exec
UPDATE orders
SET status = 'expired',
    updated_at = ?
WHERE status = 'pending_payment' AND reserved_until < ?;

-- name: DeleteReservedTicketsByOrder :exec
DELETE FROM tickets
WHERE order_id = ? AND status = 'reserved';

-- name: RejectPendingOrder :exec
UPDATE orders
SET status = 'rejected',
    updated_at = ?
WHERE id = ? AND status = 'pending_payment';

-- name: UpdateOrderMpID :exec
UPDATE orders
SET mp_order_id = ?
WHERE id = ?;

-- name: RejectExpiredOrder :exec
UPDATE orders
SET status = 'rejected',
    updated_at = ?
WHERE id = ? AND status = 'expired';

-- name: UpdateOrderPaid :exec
UPDATE orders
SET status = 'paid',
    fulfillment_status = ?,
    updated_at = ?
WHERE id = ? AND status IN ('pending_payment', 'expired', 'rejected');

-- name: UpdateOrderEmailStatus :exec
UPDATE orders
SET email_status = ?,
    email_last_error = ?,
    updated_at = ?
WHERE id = ?;
