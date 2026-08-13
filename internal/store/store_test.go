package store_test

import (
	"errors"
	"testing"

	"github.com/vesmirov/finance-api/internal/store"
	"github.com/vesmirov/finance-api/internal/testdb"
)

func testStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st := testdb.New(t)
	userID, err := st.CreateUser("tester", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	return st, userID
}

func TestCategoryCascadeDeletesExpenses(t *testing.T) {
	st, uid := testStore(t)
	catID, err := st.InsertCategory(store.Category{UserID: uid, Name: "Food", Kind: "expense", Color: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertExpense(uid, store.Expense{CategoryID: catID, Name: "Cafe", Amount: "100", Currency: "USD", Period: "monthly", MarkupPercent: "0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteCategory(uid, catID); err != nil {
		t.Fatal(err)
	}
	expenses, err := st.Expenses(uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(expenses) != 0 {
		t.Fatalf("expenses must cascade, got %d rows", len(expenses))
	}
}

func TestUserIsolation(t *testing.T) {
	st, uid := testStore(t)
	otherID, err := st.CreateUser("other", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	catID, err := st.InsertCategory(store.Category{UserID: uid, Name: "Mine", Kind: "expense", Color: 1})
	if err != nil {
		t.Fatal(err)
	}
	incID, err := st.InsertIncome(store.Income{UserID: uid, Name: "Salary", Amount: "1", Currency: "USD", Period: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	// the other user sees nothing
	if cats, _ := st.Categories(otherID); len(cats) != 0 {
		t.Fatal("categories leaked across users")
	}
	if incs, _ := st.Incomes(otherID); len(incs) != 0 {
		t.Fatal("incomes leaked across users")
	}
	// ...and cannot modify or delete foreign rows
	if err := st.UpdateIncome(otherID, incID, map[string]any{"amount": "999"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign income update: want ErrNotFound, got %v", err)
	}
	if err := st.DeleteCategory(otherID, catID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign category delete: want ErrNotFound, got %v", err)
	}
	// ...and cannot add an expense into a foreign category
	if _, err := st.InsertExpense(otherID, store.Expense{CategoryID: catID, Name: "x", Amount: "1", Currency: "USD", Period: "monthly", MarkupPercent: "0"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign expense insert: want ErrNotFound, got %v", err)
	}
}

func TestUpdateWhitelistAndNotFound(t *testing.T) {
	st, uid := testStore(t)
	id, err := st.InsertIncome(store.Income{UserID: uid, Name: "x", Amount: "1", Currency: "USD", Period: "monthly"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateIncome(uid, id, map[string]any{"amount": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateIncome(uid, id, map[string]any{"hacker": "1"}); err == nil {
		t.Fatal("non-whitelisted column must fail")
	}
	if err := st.UpdateIncome(uid, 999, map[string]any{"amount": "3"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want store.ErrNotFound, got %v", err)
	}
	if err := st.DeleteIncome(uid, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want store.ErrNotFound, got %v", err)
	}
}

func TestUserCurrencies(t *testing.T) {
	st, uid := testStore(t)
	for _, code := range []string{"USD", "THB"} {
		if err := st.AddUserCurrency(uid, code); err != nil {
			t.Fatal(err)
		}
	}
	// duplicate add is a no-op
	if err := st.AddUserCurrency(uid, "THB"); err != nil {
		t.Fatal(err)
	}
	list, err := st.UserCurrencies(uid)
	if err != nil || len(list) != 2 {
		t.Fatalf("tracked = %v, %v", list, err)
	}
	used, err := st.CurrencyReferenced(uid, "THB")
	if err != nil || used {
		t.Fatalf("THB must be unused, got %v %v", used, err)
	}
	if _, err := st.InsertIncome(store.Income{UserID: uid, Name: "x", Amount: "1", Currency: "THB", Period: "monthly"}); err != nil {
		t.Fatal(err)
	}
	used, err = st.CurrencyReferenced(uid, "THB")
	if err != nil || !used {
		t.Fatalf("THB must be used, got %v %v", used, err)
	}
	if err := st.RemoveUserCurrency(uid, "THB"); err != nil {
		t.Fatal(err)
	}
	list, _ = st.UserCurrencies(uid)
	if len(list) != 1 || list[0] != "USD" {
		t.Fatalf("tracked after remove = %v", list)
	}
}

func TestRatesUpsert(t *testing.T) {
	st, _ := testStore(t)
	if err := st.UpsertRate("EUR", "1.1543", "2026-08-14T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRate("EUR", "1.16", "2026-08-14T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	rs, err := st.Rates()
	if err != nil || len(rs) != 1 || rs[0].RateUSD != "1.16" {
		t.Fatalf("upsert failed: %v %v", rs, err)
	}
}

func TestAdminsAndUsers(t *testing.T) {
	st, uid := testStore(t)
	if n, _ := st.CountAdmins(); n != 1 {
		t.Fatalf("admins = %d, want 1", n)
	}
	regularID, err := st.CreateUser("regular", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := st.CountAdmins(); n != 1 {
		t.Fatalf("admins after regular user = %d, want 1", n)
	}
	users, err := st.Users()
	if err != nil || len(users) != 2 {
		t.Fatalf("users = %v, %v", users, err)
	}
	if err := st.UpdateUser(regularID, map[string]any{"target_currency": "EUR"}); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByID(regularID)
	if err != nil || u.TargetCurrency != "EUR" {
		t.Fatalf("target = %v, %v", u, err)
	}
	if err := st.DeleteUser(regularID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByID(regularID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user lookup: want ErrNotFound, got %v", err)
	}
	_ = uid
}
