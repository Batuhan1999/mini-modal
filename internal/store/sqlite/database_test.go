package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenWithPoolConfiguresEveryConnection(t *testing.T) {
	ctx := context.Background()
	db, err := OpenWithPool(ctx, filepath.Join(t.TempDir(), "pool.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for _, conn := range []*sql.Conn{first, second} {
		var foreignKeys, busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("connection settings: foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
		}
	}

	var firstCount, secondCount int
	if err := first.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&firstCount); err != nil {
		t.Fatal(err)
	}
	if err := second.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&secondCount); err != nil {
		t.Fatal(err)
	}
	if firstCount == 0 || firstCount != secondCount {
		t.Fatalf("connections do not share migrations: first=%d second=%d", firstCount, secondCount)
	}
}
