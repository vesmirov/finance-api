// Package testdb — hermetic PostgreSQL for tests: each test gets its own
// schema (search_path) in the shared test DB, torn down via t.Cleanup.
// Requires a running DB: `make db-up` (or the postgres service in CI).
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/vesmirov/finance-api/internal/store"
)

const defaultDSN = "postgres://finance:finance@127.0.0.1:5433/finance?sslmode=disable"

func baseDSN() string {
	if v := os.Getenv("FINANCE_TEST_DB"); v != "" {
		return v
	}
	return defaultDSN
}

// New returns a Store on top of a fresh schema with migrations applied.
func New(t *testing.T) *store.Store {
	t.Helper()

	admin, err := sql.Open("pgx", baseDSN())
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	if err := admin.Ping(); err != nil {
		t.Skipf("postgres is not available (run `make db-up`): %v", err)
	}

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(buf)
	if _, err := admin.Exec(fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema))
		_ = admin.Close()
	})

	u, err := url.Parse(baseDSN())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema)
	u.RawQuery = q.Encode()

	st, err := store.Open(u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}
