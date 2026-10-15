-- Held capacity is every ticket row. Do not filter by status.
-- name: CountTickets :one
SELECT COUNT(*) FROM tickets;

-- name: InsertTicket :one
INSERT INTO tickets (order_id, token, status) VALUES (?, ?, ?) RETURNING *;

-- name: ListTicketsByOrder :many
SELECT * FROM tickets WHERE order_id = ? ORDER BY id;

-- name: ListReservedTicketsByOrder :many
SELECT * FROM tickets WHERE order_id = ? AND status = 'reserved' ORDER BY id;

-- name: IssueReservedTicket :exec
UPDATE tickets
SET status = 'issued',
    token = ?,
    updated_at = ?
WHERE id = ? AND status = 'reserved';

-- name: GetTicketByToken :one
SELECT * FROM tickets WHERE token = ? AND token IS NOT NULL AND token <> '';

-- name: ClaimIssuedTicket :execrows
UPDATE tickets
SET status = 'used',
    used_at = ?,
    updated_at = ?
WHERE token = ? AND token <> '' AND status = 'issued';
