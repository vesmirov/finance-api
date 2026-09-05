// Package testdb — a hermetic SQLite database per test: a fresh file in
// t.TempDir() with migrations applied, removed together with the directory.
package testdb

import (
	"path/filepath"
	"testing"

	"github.com/vesmirov/finance-api/internal/store"
)

// New returns a Store on top of a fresh database with migrations applied.
func New(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(filepath.Join(t.TempDir(), "finance.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}
