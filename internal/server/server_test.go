package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/vesmirov/finance-api/internal/store"
	"github.com/vesmirov/finance-api/internal/testdb"
)

// USD-based cache, as filled by the rates runner.
var testRatesUSD = map[string]string{
	"USD": "1",
	"EUR": "1.1543",
	"GEL": "0.3690",
	"THB": "0.0261",
	"RUB": "0.01212",
}

// testClient is reset in testServer (a cookie jar with the session);
// the package tests are not parallel.
var testClient = http.DefaultClient

func newUser(t *testing.T, st *store.Store, login string, isAdmin bool) int64 {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.CreateUser(login, string(hash), isAdmin)
	if err != nil {
		t.Fatal(err)
	}
	for code := range testRatesUSD {
		if err := st.AddUserCurrency(id, code); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func signIn(t *testing.T, ts *httptest.Server, login string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	testClient = &http.Client{Jar: jar}
	if code, resp := call(t, ts, "POST", "/api/auth/login", map[string]any{
		"login": login, "password": "secret-pass",
	}); code != 200 {
		t.Fatalf("login as %s: %d %v", login, code, resp)
	}
}

func testServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st := testdb.New(t)
	for code, rate := range testRatesUSD {
		if err := st.UpsertRate(code, rate, "2026-08-14T09:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ts := httptest.NewServer(New(st, log, "test").Handler())
	t.Cleanup(ts.Close)

	newUser(t, st, "tester", true)
	signIn(t, ts, "tester")
	return ts, st
}

func call(t *testing.T, ts *httptest.Server, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

func TestFullCycle(t *testing.T) {
	ts, _ := testServer(t)

	// nett income
	code, inc := call(t, ts, "POST", "/api/incomes", map[string]any{
		"name": "Salary", "amount": "4350", "currency": "EUR",
	})
	if code != 201 {
		t.Fatalf("create income: %d %v", code, inc)
	}

	// expense category + an item with a markup
	code, cat := call(t, ts, "POST", "/api/categories", map[string]any{
		"name": "Housing", "kind": "expense",
	})
	if code != 201 {
		t.Fatalf("create category: %d %v", code, cat)
	}
	catID := int64(cat["id"].(float64))
	code, exp := call(t, ts, "POST", "/api/expenses", map[string]any{
		"category_id": catID, "name": "Utilities", "amount": "180",
		"currency": "GEL", "markup_percent": "10",
	})
	if code != 201 {
		t.Fatalf("create expense: %d %v", code, exp)
	}

	// savings
	code, sav := call(t, ts, "POST", "/api/categories", map[string]any{
		"name": "Savings", "kind": "saving",
	})
	if code != 201 {
		t.Fatalf("create saving category: %d", code)
	}
	savID := int64(sav["id"].(float64))
	if code, _ := call(t, ts, "POST", "/api/expenses", map[string]any{
		"category_id": savID, "name": "Cushion", "amount": "500", "currency": "USD",
	}); code != 201 {
		t.Fatalf("create saving expense: %d", code)
	}

	// plan: money as strings, buffer = income − expenses − savings
	code, plan := call(t, ts, "GET", "/api/plan", nil)
	if code != 200 {
		t.Fatalf("plan: %d", code)
	}
	if got := plan["income_total"]; got != "5021.21" {
		t.Errorf("income_total = %v (%T), want string \"5021.21\"", got, got)
	}
	if got := plan["expenses_total"]; got != "73.06" {
		t.Errorf("expenses_total = %v, want \"73.06\"", got)
	}
	if got := plan["savings_total"]; got != "500.00" {
		t.Errorf("savings_total = %v, want \"500.00\"", got)
	}
	if got := plan["buffer"]; got != "4448.15" {
		t.Errorf("buffer = %v, want \"4448.15\"", got)
	}

	// patching an item changes the plan
	expID := int64(exp["id"].(float64))
	if code, _ := call(t, ts, "PATCH", "/api/expenses/"+itoa(expID), map[string]any{"amount": "360"}); code != 200 {
		t.Fatalf("patch expense: %d", code)
	}
	_, plan = call(t, ts, "GET", "/api/plan", nil)
	if got := plan["expenses_total"]; got != "146.12" {
		t.Errorf("expenses_total after patch = %v, want \"146.12\"", got)
	}

	// category delete cascades
	if code, _ := call(t, ts, "DELETE", "/api/categories/"+itoa(catID), nil); code != 204 {
		t.Fatalf("delete category: %d", code)
	}
	_, plan = call(t, ts, "GET", "/api/plan", nil)
	if got := plan["expenses_total"]; got != "0.00" {
		t.Errorf("expenses_total after delete = %v, want \"0.00\"", got)
	}
}

func TestTargetChangeUsesCrossRates(t *testing.T) {
	ts, _ := testServer(t)
	call(t, ts, "POST", "/api/incomes", map[string]any{"name": "Salary", "amount": "4350", "currency": "EUR"})
	call(t, ts, "POST", "/api/incomes", map[string]any{"name": "Contract", "amount": "1500", "currency": "USD"})

	if code, _ := call(t, ts, "PATCH", "/api/settings", map[string]any{"target_currency": "EUR"}); code != 200 {
		t.Fatalf("patch settings failed")
	}
	_, settings := call(t, ts, "GET", "/api/settings", nil)
	if settings["target_currency"] != "EUR" {
		t.Fatalf("target = %v, want EUR", settings["target_currency"])
	}
	_, plan := call(t, ts, "GET", "/api/plan", nil)
	if plan["target_currency"] != "EUR" {
		t.Errorf("plan target = %v", plan["target_currency"])
	}
	incomes := plan["incomes"].([]any)
	first := incomes[0].(map[string]any)
	second := incomes[1].(map[string]any)
	// EUR income in EUR target: rate 1
	if first["equivalent"] != "4350.00" {
		t.Errorf("EUR income equivalent = %v, want 4350.00", first["equivalent"])
	}
	// USD → EUR cross rate: 1500 × (1 / 1.1543) = 1299.49
	if second["equivalent"] != "1299.49" {
		t.Errorf("USD income equivalent = %v, want 1299.49", second["equivalent"])
	}
}

func TestCurrencyGuards(t *testing.T) {
	ts, _ := testServer(t)
	// the target currency cannot be removed from tracking
	if code, _ := call(t, ts, "DELETE", "/api/currencies/USD", nil); code != 409 {
		t.Errorf("remove target: %d, want 409", code)
	}
	// a currency used by rows cannot be removed
	call(t, ts, "POST", "/api/incomes", map[string]any{"currency": "THB"})
	if code, _ := call(t, ts, "DELETE", "/api/currencies/THB", nil); code != 409 {
		t.Errorf("remove used: %d, want 409", code)
	}
	// a free one can
	if code, _ := call(t, ts, "DELETE", "/api/currencies/GEL", nil); code != 204 {
		t.Errorf("remove free: %d, want 204", code)
	}
	// a row in an untracked currency → 422
	if code, _ := call(t, ts, "POST", "/api/incomes", map[string]any{"currency": "GEL"}); code != 422 {
		t.Errorf("untracked currency row: %d, want 422", code)
	}
	// adding a currency makes it usable
	if code, _ := call(t, ts, "POST", "/api/currencies", map[string]any{"code": "gel"}); code != 201 {
		t.Errorf("add currency: want 201")
	}
	if code, _ := call(t, ts, "POST", "/api/incomes", map[string]any{"currency": "GEL"}); code != 201 {
		t.Errorf("row after re-adding currency: want 201")
	}
}

func TestColorSlots(t *testing.T) {
	ts, _ := testServer(t)
	_, first := call(t, ts, "POST", "/api/categories", map[string]any{"kind": "expense"})
	if first["color"].(float64) != 1 {
		t.Errorf("first free slot = %v, want 1", first["color"])
	}
	if code, _ := call(t, ts, "POST", "/api/categories", map[string]any{"kind": "expense", "color": 1}); code != 409 {
		t.Errorf("taken slot: %d, want 409", code)
	}
	for i := 0; i < 7; i++ {
		if code, _ := call(t, ts, "POST", "/api/categories", map[string]any{"kind": "expense"}); code != 201 {
			t.Fatalf("slot %d: %d", i+2, code)
		}
	}
	if code, _ := call(t, ts, "POST", "/api/categories", map[string]any{"kind": "expense"}); code != 409 {
		t.Errorf("9th category: %d, want 409", code)
	}
}

func TestValidation(t *testing.T) {
	ts, _ := testServer(t)
	cases := []struct {
		method, path string
		body         map[string]any
		want         int
	}{
		{"POST", "/api/incomes", map[string]any{"amount": "abc"}, 422},
		{"POST", "/api/incomes", map[string]any{"amount": "-5"}, 422},
		{"POST", "/api/incomes", map[string]any{"period": "weekly"}, 422},
		{"POST", "/api/incomes", map[string]any{"currency": "DOGE"}, 422},
		{"POST", "/api/categories", map[string]any{"kind": "fund"}, 422},
		{"POST", "/api/expenses", map[string]any{"name": "x"}, 422},
		{"PATCH", "/api/incomes/999", map[string]any{"amount": "1"}, 404},
		{"POST", "/api/currencies", map[string]any{"code": "DOGECOIN"}, 422},
	}
	for _, c := range cases {
		if code, _ := call(t, ts, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %v = %d, want %d", c.method, c.path, c.body, code, c.want)
		}
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
