package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/vesmirov/finance-api/internal/store"
)

const minPasswordLen = 8

// runAdmin — account management from the CLI (django manage.py analogue):
//
//	finance admin create   [-login X] [-password Y]   create an admin
//	finance admin password [-login X] [-password Y]   change the password (sessions are revoked)
//
// Without -password the password is asked interactively (hidden input with confirmation);
// without a TTY it is read as a single line from stdin.
func runAdmin(st *store.Store, action, login, password string) error {
	reader := bufio.NewReader(os.Stdin)

	if login == "" {
		fmt.Print("Login: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		login = strings.TrimSpace(line)
	}
	if login == "" {
		return errors.New("login must not be empty")
	}

	if password == "" {
		var err error
		password, err = readPassword(reader)
		if err != nil {
			return err
		}
	}
	if len(password) < minPasswordLen {
		return fmt.Errorf("password is shorter than %d characters", minPasswordLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	switch action {
	case "create":
		if _, err := st.UserByLogin(login); err == nil {
			return fmt.Errorf("user %q already exists (to change the password: finance admin password)", login)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if _, err := st.CreateUser(login, string(hash), true); err != nil {
			return err
		}
		fmt.Printf("admin created: %s\n", login)
	case "password":
		u, err := st.UserByLogin(login)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("user %q not found", login)
			}
			return err
		}
		if err := st.UpdateUser(u.ID, map[string]any{"password_hash": string(hash)}); err != nil {
			return err
		}
		if err := st.DeleteUserSessions(u.ID); err != nil {
			return err
		}
		fmt.Printf("password updated: %s (active sessions revoked)\n", login)
	default:
		return fmt.Errorf("unknown action %q: expected create or password", action)
	}
	return nil
}

func readPassword(fallback *bufio.Reader) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := fallback.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Print("Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("Repeat: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}
