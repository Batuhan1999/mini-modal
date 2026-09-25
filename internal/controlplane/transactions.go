package controlplane

import (
	"context"
	"database/sql"
	"fmt"

	sqlitedb "mini-modal/internal/store/sqlite/sqlc"
)

func withTx[T any](
	ctx context.Context,
	db *sql.DB,
	operation func(*sqlitedb.Queries) (T, error),
) (T, error) {
	var zero T

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := operation(sqlitedb.New(tx))
	if err != nil {
		return zero, err
	}

	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("commit transaction: %w", err)
	}

	return result, nil
}
