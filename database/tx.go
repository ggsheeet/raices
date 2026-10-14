package database

import (
	"context"

	"raices/database/sqlc"
)

func (db *DB) InTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := db.Queries.WithTx(tx)
	if err := fn(q); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
