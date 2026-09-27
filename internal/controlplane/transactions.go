package controlplane

import (
	"context"
	"database/sql"
	"fmt"

	postgresdb "mini-modal/internal/store/postgres/sqlc"
)

func withTx[T any](
	ctx context.Context,
	db *sql.DB,
	operation func(*postgresdb.Queries) (T, error),
) (T, error) {
	var zero T

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := operation(postgresdb.New(tx))
	if err != nil {
		return zero, err
	}

	if err := tx.Commit(); err != nil {
		return zero, fmt.Errorf("commit transaction: %w", err)
	}

	return result, nil
}
