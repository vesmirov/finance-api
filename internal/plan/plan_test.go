package plan

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func v7cRates() map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		"EUR": d("1.1543"),
		"GEL": d("0.3690"),
		"THB": d("0.0261"),
		"RUB": d("0.01212"),
	}
}

// v7cInput — the reference dataset from the docs/design/finance-v7c.dc.html
// prototype (numbers verified by the user by hand; see ARCHITECTURE §6 and §11).
func v7cInput() Input {
	return Input{
		Target: "USD",
		Rates:  v7cRates(),
		Incomes: []Income{
			{ID: 1, Name: "Salary", Amount: d("4350"), Currency: "EUR", Period: PeriodMonthly},
			{ID: 2, Name: "Contract", Amount: d("1500"), Currency: "USD", Period: PeriodMonthly},
			{ID: 3, Name: "Dividends", Amount: d("170000"), Currency: "RUB", Period: PeriodMonthly},
		},
		Categories: []Category{
			{ID: 1, Name: "Housing", Kind: KindExpense, Color: 1, Expenses: []Expense{
				{Name: "Rent", Amount: d("2500"), Currency: "GEL", Period: PeriodMonthly, Markup: d("0")},
				{Name: "Utilities", Amount: d("180"), Currency: "GEL", Period: PeriodMonthly, Markup: d("10")},
			}},
			{ID: 2, Name: "Subscriptions", Kind: KindExpense, Color: 2, Expenses: []Expense{
				{Name: "Cloud services", Amount: d("100"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
				{Name: "1Password", Amount: d("57"), Currency: "EUR", Period: PeriodAnnual, Markup: d("0")},
				{Name: "Telegram", Amount: d("29"), Currency: "USD", Period: PeriodAnnual, Markup: d("0")},
				{Name: "Obsidian Sync Family Plan", Amount: d("48"), Currency: "USD", Period: PeriodAnnual, Markup: d("0")},
				{Name: "YouTube", Amount: d("3.50"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
			}},
			{ID: 3, Name: "Food", Kind: KindExpense, Color: 3, Expenses: []Expense{
				{Name: "Groceries", Amount: d("25000"), Currency: "THB", Period: PeriodMonthly, Markup: d("5")},
				{Name: "Cafe", Amount: d("8000"), Currency: "THB", Period: PeriodMonthly, Markup: d("0")},
			}},
			{ID: 4, Name: "Transport", Kind: KindExpense, Color: 4, Expenses: []Expense{
				{Name: "Taxi", Amount: d("3000"), Currency: "THB", Period: PeriodMonthly, Markup: d("0")},
			}},
			{ID: 5, Name: "Entertainment", Kind: KindExpense, Color: 7, Expenses: []Expense{
				{Name: "Leisure", Amount: d("400"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
			}},
			{ID: 6, Name: "Health", Kind: KindExpense, Color: 6},
			{ID: 7, Name: "Savings", Kind: KindSaving, Color: 5, Expenses: []Expense{
				{Name: "Investments", Amount: d("1500"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
				{Name: "Cushion", Amount: d("500"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
			}},
		},
	}
}

func assertEq(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(d(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func assertShare(t *testing.T, name string, got *decimal.Decimal, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %s", name, want)
	}
	if !got.Equal(d(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}

func TestComputeV7CFixture(t *testing.T) {
	s := Compute(v7cInput())

	assertEq(t, "income_total", s.IncomeTotal, "8581.61")
	assertEq(t, "expenses_total", s.ExpensesTotal, "2483.19")
	assertEq(t, "savings_total", s.SavingsTotal, "2000")
	assertEq(t, "buffer", s.Buffer, "4098.42")
	assertShare(t, "expenses_share", s.ExpensesShare, "0.2894")
	assertShare(t, "savings_share", s.SavingsShare, "0.2331")
	assertShare(t, "buffer_share", s.BufferShare, "0.4776")
	if len(s.MissingRates) != 0 {
		t.Errorf("missing_rates = %v, want empty", s.MissingRates)
	}

	byName := map[string]CategoryOut{}
	for _, c := range s.Categories {
		byName[c.Name] = c
	}
	assertEq(t, "Housing", byName["Housing"].Total, "995.56")
	assertEq(t, "Subscriptions", byName["Subscriptions"].Total, "115.40")
	assertEq(t, "Food", byName["Food"].Total, "893.93")
	assertEq(t, "Transport", byName["Transport"].Total, "78.30")
	assertEq(t, "Entertainment", byName["Entertainment"].Total, "400")
	assertEq(t, "Health", byName["Health"].Total, "0")
	assertEq(t, "Savings", byName["Savings"].Total, "2000")
	assertShare(t, "Housing share", byName["Housing"].ShareOfIncome, "0.1160")
	assertShare(t, "Subscriptions share", byName["Subscriptions"].ShareOfIncome, "0.0134")

	// per-line equivalents
	assertEq(t, "Salary eq", s.Incomes[0].Equivalent, "5021.21")
	assertEq(t, "Dividends eq", s.Incomes[2].Equivalent, "2060.40")
	subs := byName["Subscriptions"].Expenses
	assertEq(t, "1Password monthly", subs[1].Monthly, "4.75")
	assertEq(t, "1Password eq", subs[1].Equivalent, "5.48")
	assertEq(t, "Telegram eq", subs[2].Equivalent, "2.42")
	food := byName["Food"].Expenses
	assertEq(t, "Groceries eq (+5%)", food[0].Equivalent, "685.13")
	housing := byName["Housing"].Expenses
	assertEq(t, "Utilities eq (+10%)", housing[1].Equivalent, "73.06")
}

func TestMonthlyAnnualDivision(t *testing.T) {
	cases := []struct{ in, want string }{
		{"57", "4.75"},
		{"29", "2.42"},
		{"3490", "290.83"},
		{"100", "8.33"},
	}
	for _, c := range cases {
		if got := Monthly(d(c.in), PeriodAnnual); !got.Equal(d(c.want)) {
			t.Errorf("Monthly(%s, annual) = %s, want %s", c.in, got, c.want)
		}
	}
	if got := Monthly(d("100"), PeriodMonthly); !got.Equal(d("100")) {
		t.Errorf("Monthly(100, monthly) = %s, want 100", got)
	}
}

func TestMissingRateContributesZero(t *testing.T) {
	in := Input{
		Target: "USD",
		Rates:  map[string]decimal.Decimal{},
		Incomes: []Income{
			{Name: "ok", Amount: d("100"), Currency: "USD", Period: PeriodMonthly},
		},
		Categories: []Category{
			{Name: "c", Kind: KindExpense, Color: 1, Expenses: []Expense{
				{Name: "no rate", Amount: d("500"), Currency: "KZT", Period: PeriodMonthly, Markup: d("0")},
			}},
		},
	}
	s := Compute(in)
	assertEq(t, "expenses_total", s.ExpensesTotal, "0")
	assertEq(t, "buffer", s.Buffer, "100")
	if len(s.MissingRates) != 1 || s.MissingRates[0] != "KZT" {
		t.Errorf("missing_rates = %v, want [KZT]", s.MissingRates)
	}
	if !s.Categories[0].Expenses[0].MissingRate {
		t.Error("expense line must be marked MissingRate")
	}
}

func TestZeroIncomeSharesNil(t *testing.T) {
	s := Compute(Input{Target: "USD"})
	if s.ExpensesShare != nil || s.SavingsShare != nil || s.BufferShare != nil {
		t.Error("shares must be nil when income is zero")
	}
}

func TestNegativeBuffer(t *testing.T) {
	in := Input{
		Target: "USD",
		Incomes: []Income{
			{Name: "i", Amount: d("100"), Currency: "USD", Period: PeriodMonthly},
		},
		Categories: []Category{
			{Name: "c", Kind: KindExpense, Color: 1, Expenses: []Expense{
				{Name: "e", Amount: d("412.40"), Currency: "USD", Period: PeriodMonthly, Markup: d("0")},
			}},
		},
	}
	s := Compute(in)
	assertEq(t, "buffer", s.Buffer, "-312.40")
}

func TestFractionalMarkup(t *testing.T) {
	in := Input{
		Target: "USD",
		Incomes: []Income{
			{Name: "i", Amount: d("1000"), Currency: "USD", Period: PeriodMonthly},
		},
		Categories: []Category{
			{Name: "c", Kind: KindExpense, Color: 1, Expenses: []Expense{
				{Name: "e", Amount: d("100"), Currency: "USD", Period: PeriodMonthly, Markup: d("2.5")},
			}},
		},
	}
	s := Compute(in)
	assertEq(t, "eq +2.5%", s.Categories[0].Expenses[0].Equivalent, "102.50")
}
