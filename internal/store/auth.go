package store

import (
	"database/sql"
	"errors"
	"time"
)

type User struct {
	ID             int64
	Login          string
	PasswordHash   string
	IsAdmin        bool
	TargetCurrency string
}

const userCols = `id, login, password_hash, is_admin, target_currency`

func scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Login, &u.PasswordHash, &u.IsAdmin, &u.TargetCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Login, &u.PasswordHash, &u.IsAdmin, &u.TargetCurrency); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UserByLogin(login string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE login = $1`, login))
}

func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) CreateUser(login, passwordHash string, isAdmin bool) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`INSERT INTO users (login, password_hash, is_admin) VALUES ($1, $2, $3) RETURNING id`,
		login, passwordHash, isAdmin).Scan(&id)
	return id, err
}

var userColumns = map[string]bool{
	"login": true, "password_hash": true, "is_admin": true, "target_currency": true,
}

func (s *Store) UpdateUser(id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	// users are not owner-scoped: reuse the generic builder with a no-op condition
	return s.update(id, "users", `$%d::bigint = id`, id, fields, userColumns)
}

func (s *Store) DeleteUser(id int64) error {
	return s.delete("users", `id = $1`, id)
}

func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin`).Scan(&n)
	return n, err
}

// ── sessions ────────────────────────────────────────────────────────────────

func (s *Store) CreateSession(tokenHash string, userID int64, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return err
}

// SessionUser returns the user of a live session; expired ones are cleaned up
// along the way.
func (s *Store) SessionUser(tokenHash string) (*User, error) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at < now()`)
	var userID int64
	err := s.db.QueryRow(
		`SELECT user_id FROM sessions WHERE token_hash = $1 AND expires_at >= now()`,
		tokenHash).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.UserByID(userID)
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}
