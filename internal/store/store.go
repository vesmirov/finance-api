// Package store — the SQL layer on top of SQLite: connection, migrations,
// queries. Money and percents are stored as decimal strings (TEXT); no
// arithmetic happens here. All plan data is scoped by user.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a row with the given id does not exist
// (or is not owned by the given user).
var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database file at path; a missing parent
// directory is created. Every connection gets WAL journaling, a busy timeout
// and foreign-key enforcement (the schema relies on ON DELETE CASCADE).
// A single pooled connection serialises writers, so "database is locked"
// never surfaces; every query in this package consumes its rows before the
// next one runs, which that requires.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	// url.URL percent-encodes '%', '?' and '#' in the path, which SQLite's
	// URI parser would otherwise interpret.
	dsn := url.URL{Scheme: "file", Path: abs, RawQuery: "_busy_timeout=5000&_foreign_keys=1&_journal_mode=WAL"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", abs, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Migrate() error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return goose.Up(s.db, "migrations")
}

// ── per-user tracked currencies ─────────────────────────────────────────────

func (s *Store) UserCurrencies(userID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT code FROM user_currencies WHERE user_id = $1 ORDER BY code`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		out = append(out, code)
	}
	return out, rows.Err()
}

func (s *Store) AddUserCurrency(userID int64, code string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_currencies (user_id, code) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, code)
	return err
}

func (s *Store) RemoveUserCurrency(userID int64, code string) error {
	_, err := s.db.Exec(
		`DELETE FROM user_currencies WHERE user_id = $1 AND code = $2`, userID, code)
	return err
}

// CurrencyReferenced — is the currency used by the user's income or expense rows.
func (s *Store) CurrencyReferenced(userID int64, code string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT (SELECT COUNT(*) FROM incomes WHERE user_id = $1 AND currency = $2) +
		        (SELECT COUNT(*) FROM expenses e JOIN categories c ON c.id = e.category_id
		         WHERE c.user_id = $1 AND e.currency = $2)`, userID, code).Scan(&n)
	return n > 0, err
}

// ── rates: global USD-based cache ───────────────────────────────────────────

// Rate holds how many USD one unit of Code costs (decimal string).
type Rate struct {
	Code      string
	RateUSD   string
	FetchedAt string
}

func (s *Store) Rates() ([]Rate, error) {
	rows, err := s.db.Query(`SELECT code, rate_usd, fetched_at FROM rates ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rate
	for rows.Next() {
		var r Rate
		if err := rows.Scan(&r.Code, &r.RateUSD, &r.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpsertRate(code, rateUSD, fetchedAt string) error {
	_, err := s.db.Exec(
		`INSERT INTO rates (code, rate_usd, fetched_at) VALUES ($1, $2, $3)
		 ON CONFLICT (code) DO UPDATE SET rate_usd = excluded.rate_usd, fetched_at = excluded.fetched_at`,
		code, rateUSD, fetchedAt)
	return err
}

// ── incomes (user-scoped) ───────────────────────────────────────────────────

type Income struct {
	ID       int64
	UserID   int64
	Name     string
	Amount   string
	Currency string
	Period   string
	Position int
}

func (s *Store) Incomes(userID int64) ([]Income, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, amount, currency, period, position
		 FROM incomes WHERE user_id = $1 ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Income
	for rows.Next() {
		var i Income
		if err := rows.Scan(&i.ID, &i.UserID, &i.Name, &i.Amount, &i.Currency, &i.Period, &i.Position); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) InsertIncome(i Income) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO incomes (user_id, name, amount, currency, period, position)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		i.UserID, i.Name, i.Amount, i.Currency, i.Period, i.Position).Scan(&id)
	return id, err
}

var incomeColumns = map[string]bool{"name": true, "amount": true, "currency": true, "period": true, "position": true}

func (s *Store) UpdateIncome(userID, id int64, fields map[string]any) error {
	return s.update(userID, "incomes", `user_id = $%d`, id, fields, incomeColumns)
}

func (s *Store) DeleteIncome(userID, id int64) error {
	return s.delete("incomes", `id = $1 AND user_id = $2`, id, userID)
}

// ── categories (user-scoped) ────────────────────────────────────────────────

type Category struct {
	ID       int64
	UserID   int64
	Name     string
	Kind     string
	Color    int
	Position int
}

func (s *Store) Categories(userID int64) ([]Category, error) {
	rows, err := s.db.Query(
		`SELECT id, user_id, name, kind, color, position
		 FROM categories WHERE user_id = $1 ORDER BY position, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.Kind, &c.Color, &c.Position); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) InsertCategory(c Category) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO categories (user_id, name, kind, color, position)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		c.UserID, c.Name, c.Kind, c.Color, c.Position).Scan(&id)
	return id, err
}

var categoryColumns = map[string]bool{"name": true, "color": true, "position": true}

func (s *Store) UpdateCategory(userID, id int64, fields map[string]any) error {
	return s.update(userID, "categories", `user_id = $%d`, id, fields, categoryColumns)
}

func (s *Store) DeleteCategory(userID, id int64) error {
	return s.delete("categories", `id = $1 AND user_id = $2`, id, userID)
}

// ── expenses (scoped through the owning category) ───────────────────────────

type Expense struct {
	ID            int64
	CategoryID    int64
	Name          string
	Amount        string
	Currency      string
	Period        string
	Day           *int
	MarkupPercent string
	Position      int
}

func (s *Store) Expenses(userID int64) ([]Expense, error) {
	rows, err := s.db.Query(
		`SELECT e.id, e.category_id, e.name, e.amount, e.currency, e.period, e.day, e.markup_percent, e.position
		 FROM expenses e JOIN categories c ON c.id = e.category_id
		 WHERE c.user_id = $1 ORDER BY e.position, e.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Expense
	for rows.Next() {
		var e Expense
		if err := rows.Scan(&e.ID, &e.CategoryID, &e.Name, &e.Amount, &e.Currency, &e.Period, &e.Day, &e.MarkupPercent, &e.Position); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertExpense checks that the category belongs to the user.
func (s *Store) InsertExpense(userID int64, e Expense) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO expenses (category_id, name, amount, currency, period, day, markup_percent, position)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8
		 WHERE EXISTS (SELECT 1 FROM categories WHERE id = $1 AND user_id = $9)
		 RETURNING id`,
		e.CategoryID, e.Name, e.Amount, e.Currency, e.Period, e.Day, e.MarkupPercent, e.Position, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

var expenseColumns = map[string]bool{
	"category_id": true, "name": true, "amount": true, "currency": true,
	"period": true, "day": true, "markup_percent": true, "position": true,
}

func (s *Store) UpdateExpense(userID, id int64, fields map[string]any) error {
	return s.update(userID, "expenses",
		`category_id IN (SELECT id FROM categories WHERE user_id = $%d)`, id, fields, expenseColumns)
}

func (s *Store) DeleteExpense(userID, id int64) error {
	return s.delete("expenses",
		`id = $1 AND category_id IN (SELECT id FROM categories WHERE user_id = $2)`, id, userID)
}

// ── generic helpers ─────────────────────────────────────────────────────────

// update builds SET from a column whitelist; ownerCond is a fmt template for
// the ownership predicate receiving the position of the userID argument.
func (s *Store) update(userID int64, table, ownerCond string, id int64, fields map[string]any, allowed map[string]bool) error {
	if len(fields) == 0 {
		return nil
	}
	assignments := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields)+2)
	for col, val := range fields {
		if !allowed[col] {
			return fmt.Errorf("column %q is not updatable", col)
		}
		args = append(args, val)
		assignments = append(assignments, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	args = append(args, id)
	idPos := len(args)
	args = append(args, userID)
	cond := fmt.Sprintf(ownerCond, len(args))
	res, err := s.db.Exec(
		fmt.Sprintf(`UPDATE %s SET %s WHERE id = $%d AND %s`,
			table, strings.Join(assignments, ", "), idPos, cond), args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) delete(table, cond string, args ...any) error {
	res, err := s.db.Exec(fmt.Sprintf(`DELETE FROM %s WHERE %s`, table, cond), args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
