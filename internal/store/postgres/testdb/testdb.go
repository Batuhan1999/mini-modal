package testdb

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"mini-modal/internal/store/postgres"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const FunctionVersionID = "test-function-version"

func SeedFunctionVersion(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSERT INTO functions (id, created_at) VALUES ('test-function', 0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO function_versions (
            id, function_id, artifact_key, image, entrypoint,
            gpus, cpus, memory_gb, timeout_seconds, max_attempts, retry_backoff_seconds, created_at
        ) VALUES ($1, 'test-function', 'test-artifact', 'python:3.13-slim', 'hello_function:greet', 0, 1, 1, 0, 1, 0, 0)
	`, FunctionVersionID); err != nil {
		t.Fatal(err)
	}
}

func Open(t *testing.T) *sql.DB {
	db, _ := OpenWithDSN(t)
	return db
}

func OpenWithDSN(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("MINI_MODAL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MINI_MODAL_TEST_DATABASE_URL to run Postgres integration tests")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := postgres.Open(ctx, u.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, u.String()
}
