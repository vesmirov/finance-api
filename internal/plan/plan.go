// Package plan — the calculation core of the budget planner: pure functions, no DB or HTTP.
// Money rules — ARCHITECTURE §6 invariants:
//   - all amounts are decimal, float never appears anywhere;
//   - expense: amount → monthly (annual ÷12, quantized to 0.01) → ×(1+markup/100) → ×rate → quantized to 0.01;
//   - income:  amount (nett) → monthly → ×rate → quantized to 0.01;
//   - rate = target units per 1 unit of the line's currency; a missing rate:
//     contribution 0 + the currency goes into MissingRates;
//   - buffer = income − expenses − savings, may be negative;
//   - shares — 4 decimal places, nil when income is zero.
package plan

import (
	"sort"

	"github.com/shopspring/decimal"
)

const (
	PeriodMonthly = "monthly"
	PeriodAnnual  = "annual"

	KindExpense = "expense"
	KindSaving  = "saving"
)

var (
	twelve  = decimal.NewFromInt(12)
	hundred = decimal.NewFromInt(100)
	one     = decimal.NewFromInt(1)
)

type Income struct {
	ID       int64
	Name     string
	Amount   decimal.Decimal // nett
	Currency string
	Period   string
}

type Expense struct {
	ID       int64
	Name     string
	Amount   decimal.Decimal
	Currency string
	Period   string
	Day      *int
	Markup   decimal.Decimal // percent, e.g. 10 or 2.5
}

type Category struct {
	ID       int64
	Name     string
	Kind     string
	Color    int
	Expenses []Expense
}

type Input struct {
	Target     string
	Rates      map[string]decimal.Decimal // code → target units per 1 unit
	Incomes    []Income
	Categories []Category
}

type IncomeLine struct {
	Income
	Monthly     decimal.Decimal
	Equivalent  decimal.Decimal
	MissingRate bool
}

type ExpenseLine struct {
	Expense
	Monthly     decimal.Decimal
	Equivalent  decimal.Decimal
	MissingRate bool
}

type CategoryOut struct {
	ID            int64
	Name          string
	Kind          string
	Color         int
	Total         decimal.Decimal
	ShareOfIncome *decimal.Decimal
	Expenses      []ExpenseLine
}

type Summary struct {
	Target        string
	IncomeTotal   decimal.Decimal
	ExpensesTotal decimal.Decimal
	SavingsTotal  decimal.Decimal
	Buffer        decimal.Decimal
	ExpensesShare *decimal.Decimal
	SavingsShare  *decimal.Decimal
	BufferShare   *decimal.Decimal
	MissingRates  []string
	Incomes       []IncomeLine
	Categories    []CategoryOut
}

// Monthly normalizes an amount to monthly: an annual one is divided by 12 and
// quantized to 0.01 half-up — deliberately not rounded to whole currency units.
func Monthly(amount decimal.Decimal, period string) decimal.Decimal {
	if period == PeriodAnnual {
		return amount.Div(twelve).Round(2)
	}
	return amount
}

func share(part, whole decimal.Decimal) *decimal.Decimal {
	if whole.Sign() <= 0 {
		return nil
	}
	v := part.Div(whole).Round(4)
	return &v
}

func Compute(in Input) Summary {
	missing := map[string]bool{}
	rate := func(currency string) (decimal.Decimal, bool) {
		if currency == in.Target {
			return one, true
		}
		r, ok := in.Rates[currency]
		return r, ok
	}

	incomeTotal := decimal.Zero
	incomes := make([]IncomeLine, 0, len(in.Incomes))
	for _, inc := range in.Incomes {
		line := IncomeLine{Income: inc, Monthly: Monthly(inc.Amount, inc.Period)}
		if r, ok := rate(inc.Currency); ok {
			line.Equivalent = line.Monthly.Mul(r).Round(2)
		} else {
			line.MissingRate = true
			missing[inc.Currency] = true
		}
		incomeTotal = incomeTotal.Add(line.Equivalent)
		incomes = append(incomes, line)
	}

	expensesTotal, savingsTotal := decimal.Zero, decimal.Zero
	cats := make([]CategoryOut, 0, len(in.Categories))
	for _, c := range in.Categories {
		out := CategoryOut{ID: c.ID, Name: c.Name, Kind: c.Kind, Color: c.Color,
			Total: decimal.Zero, Expenses: make([]ExpenseLine, 0, len(c.Expenses))}
		for _, e := range c.Expenses {
			line := ExpenseLine{Expense: e, Monthly: Monthly(e.Amount, e.Period)}
			if r, ok := rate(e.Currency); ok {
				factor := one.Add(e.Markup.Div(hundred))
				line.Equivalent = line.Monthly.Mul(factor).Mul(r).Round(2)
			} else {
				line.MissingRate = true
				missing[e.Currency] = true
			}
			out.Total = out.Total.Add(line.Equivalent)
			out.Expenses = append(out.Expenses, line)
		}
		switch c.Kind {
		case KindSaving:
			savingsTotal = savingsTotal.Add(out.Total)
		default:
			expensesTotal = expensesTotal.Add(out.Total)
		}
		cats = append(cats, out)
	}

	for i := range cats {
		cats[i].ShareOfIncome = share(cats[i].Total, incomeTotal)
	}

	buffer := incomeTotal.Sub(expensesTotal).Sub(savingsTotal)

	missingList := make([]string, 0, len(missing))
	for code := range missing {
		missingList = append(missingList, code)
	}
	sort.Strings(missingList)

	return Summary{
		Target:        in.Target,
		IncomeTotal:   incomeTotal,
		ExpensesTotal: expensesTotal,
		SavingsTotal:  savingsTotal,
		Buffer:        buffer,
		ExpensesShare: share(expensesTotal, incomeTotal),
		SavingsShare:  share(savingsTotal, incomeTotal),
		BufferShare:   share(buffer, incomeTotal),
		MissingRates:  missingList,
		Incomes:       incomes,
		Categories:    cats,
	}
}
