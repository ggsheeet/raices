-- Held capacity is every ticket row. Do not filter by status.
-- name: CountTickets :one
SELECT COUNT(*) FROM tickets;
