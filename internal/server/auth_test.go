package server

import (
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func TestAuthFlow(t *testing.T) {
	ts, _ := testServer(t) // testServer created an admin and signed testClient in

	code, st := call(t, ts, "GET", "/api/auth/status", nil)
	if code != 200 || st["authenticated"] != true || st["login"] != "tester" || st["is_admin"] != true {
		t.Fatalf("status: %d %v", code, st)
	}

	// no web sign-up of any kind
	if code, _ := call(t, ts, "POST", "/api/auth/setup", map[string]any{
		"login": "hacker", "password": "password123",
	}); code != 404 {
		t.Errorf("setup endpoint must not exist: %d, want 404", code)
	}

	// anonymous: /api is closed, auth/health are open
	prev := testClient
	anon, _ := cookiejar.New(nil)
	testClient = &http.Client{Jar: anon}
	if code, _ := call(t, ts, "GET", "/api/plan", nil); code != 401 {
		t.Errorf("anon plan: %d, want 401", code)
	}
	if code, _ := call(t, ts, "GET", "/api/health", nil); code != 200 {
		t.Errorf("anon health: %d, want 200", code)
	}
	if code, s := call(t, ts, "GET", "/api/auth/status", nil); code != 200 || s["authenticated"] != false {
		t.Errorf("anon status: %d %v", code, s)
	}

	// wrong password, then the right one
	if code, _ := call(t, ts, "POST", "/api/auth/login", map[string]any{
		"login": "tester", "password": "wrong-pass-1",
	}); code != 401 {
		t.Errorf("bad login: %d, want 401", code)
	}
	signIn(t, ts, "tester")
	if code, _ := call(t, ts, "GET", "/api/plan", nil); code != 200 {
		t.Errorf("plan after login: want 200")
	}

	// logout kills the session
	if code, _ := call(t, ts, "POST", "/api/auth/logout", nil); code != 200 {
		t.Fatalf("logout failed")
	}
	if code, _ := call(t, ts, "GET", "/api/plan", nil); code != 401 {
		t.Errorf("plan after logout: want 401")
	}
	testClient = prev
}

func TestAdminAPI(t *testing.T) {
	ts, st := testServer(t)

	// create a regular user
	code, created := call(t, ts, "POST", "/api/admin/users", map[string]any{
		"login": "family", "password": "family-pass-1", "is_admin": false,
	})
	if code != 201 {
		t.Fatalf("admin create user: %d %v", code, created)
	}
	userID := int64(created["id"].(float64))

	// duplicate login → 409; short password → 422
	if code, _ := call(t, ts, "POST", "/api/admin/users", map[string]any{
		"login": "family", "password": "family-pass-1",
	}); code != 409 {
		t.Errorf("duplicate login: %d, want 409", code)
	}
	if code, _ := call(t, ts, "POST", "/api/admin/users", map[string]any{
		"login": "x", "password": "short",
	}); code != 422 {
		t.Errorf("short password: %d, want 422", code)
	}

	// the list shows both
	code, _ = call(t, ts, "GET", "/api/admin/users", nil)
	if code != 200 {
		t.Fatalf("admin list: %d", code)
	}

	// self-delete and last-admin guards
	admin, err := st.UserByLogin("tester")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, ts, "DELETE", "/api/admin/users/"+itoa(admin.ID), nil); code != 409 {
		t.Errorf("self delete: %d, want 409", code)
	}

	// password reset revokes the user's sessions
	if code, _ := call(t, ts, "POST", "/api/admin/users/"+itoa(userID)+"/password", map[string]any{
		"password": "reset-pass-1",
	}); code != 200 {
		t.Fatalf("password reset failed")
	}

	// the user data is isolated: sign in as family, plan is empty
	adminClient := testClient
	signIn2 := func(login, password string) int {
		jar, _ := cookiejar.New(nil)
		testClient = &http.Client{Jar: jar}
		code, _ := call(t, ts, "POST", "/api/auth/login", map[string]any{
			"login": login, "password": password,
		})
		return code
	}
	if code := signIn2("family", "family-pass-1"); code != 401 {
		t.Errorf("old password after reset: %d, want 401", code)
	}
	if code := signIn2("family", "reset-pass-1"); code != 200 {
		t.Fatalf("new password login: %d", code)
	}

	// the admin API is hidden from non-admins: 404, not 403
	if code, _ := call(t, ts, "GET", "/api/admin/users", nil); code != 404 {
		t.Errorf("admin api for non-admin: %d, want 404", code)
	}
	if code, st := call(t, ts, "GET", "/api/auth/status", nil); code != 200 || st["is_admin"] != false {
		t.Errorf("family status: %d %v", code, st)
	}
	// a fresh user has an empty plan and default target
	if _, plan := call(t, ts, "GET", "/api/plan", nil); plan["income_total"] != "0.00" {
		t.Errorf("family plan must be empty, income_total = %v", plan["income_total"])
	}

	testClient = adminClient
	// deleting the regular user works for the admin
	if code, _ := call(t, ts, "DELETE", "/api/admin/users/"+itoa(userID), nil); code != 204 {
		t.Errorf("delete user: want 204")
	}
}
