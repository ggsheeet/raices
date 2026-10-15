-- name: InsertSession :exec
INSERT INTO sessions (token_hash, expires_at) VALUES (?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = ?;

-- name: CountSessions :one
SELECT COUNT(*) FROM sessions;
