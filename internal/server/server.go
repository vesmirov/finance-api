// Package server — the HTTP API: /api routes, JSON (money as strings),
// middleware (auth, logging, recover). Calculations live in internal/plan.
// All plan data is scoped to the session user; /api/admin/* is admin-only
// and answers 404 to everyone else so its existence is not revealed.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/vesmirov/finance-api/internal/plan"
	"github.com/vesmirov/finance-api/internal/store"
)

type Server struct {
	st      *store.Store
	log     *slog.Logger
	version string
}

func New(st *store.Store, log *slog.Logger, version string) *Server {
	return &Server{st: st, log: log, version: version}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": "finance", "health": "/api/health"})
	})
	mux.HandleFunc("GET /api/health", s.health)

	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)

	mux.HandleFunc("GET /api/plan", s.getPlan)

	mux.HandleFunc("POST /api/incomes", s.createIncome)
	mux.HandleFunc("PATCH /api/incomes/{id}", s.patchIncome)
	mux.HandleFunc("DELETE /api/incomes/{id}", s.deleteIncome)

	mux.HandleFunc("GET /api/categories", s.listCategories)
	mux.HandleFunc("POST /api/categories", s.createCategory)
	mux.HandleFunc("PATCH /api/categories/{id}", s.patchCategory)
	mux.HandleFunc("DELETE /api/categories/{id}", s.deleteCategory)

	mux.HandleFunc("POST /api/expenses", s.createExpense)
	mux.HandleFunc("PATCH /api/expenses/{id}", s.patchExpense)
	mux.HandleFunc("DELETE /api/expenses/{id}", s.deleteExpense)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PATCH /api/settings", s.patchSettings)

	mux.HandleFunc("GET /api/currencies", s.listCurrencies)
	mux.HandleFunc("POST /api/currencies", s.addCurrency)
	mux.HandleFunc("DELETE /api/currencies/{code}", s.removeCurrency)

	mux.HandleFunc("GET /api/rates", s.getRates)

	mux.HandleFunc("GET /api/admin/users", s.adminOnly(s.adminListUsers))
	mux.HandleFunc("POST /api/admin/users", s.adminOnly(s.adminCreateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.adminOnly(s.adminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/password", s.adminOnly(s.adminResetPassword))

	return s.withRecover(s.withLog(s.requireAuth(mux)))
}

// ── middleware ──────────────────────────────────────────────────────────────

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur", time.Since(start).Round(time.Millisecond).String())
	})
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic", "err", rec, "path", r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── JSON helpers ────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func (s *Server) storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.log.Error("store", "err", err)
	writeErr(w, http.StatusInternalServerError, "storage error")
}

// ── session user in context ─────────────────────────────────────────────────

type ctxKey int

const userKey ctxKey = 0

func withUser(r *http.Request, u *store.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userKey, u))
}

// user returns the authenticated user; requireAuth guarantees it is present
// on every protected route.
func (s *Server) user(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

// ── plan assembly ───────────────────────────────────────────────────────────

// buildPlan converts the global USD cache into target-relative rates and runs
// the pure calculation core for the given user.
func (s *Server) buildPlan(u *store.User) (*plan.Summary, error) {
	target := u.TargetCurrency
	stored, err := s.st.Rates()
	if err != nil {
		return nil, err
	}
	usd := make(map[string]decimal.Decimal, len(stored))
	for _, r := range stored {
		if d, err := decimal.NewFromString(r.RateUSD); err == nil && d.Sign() > 0 {
			usd[r.Code] = d
		}
	}
	rateMap := map[string]decimal.Decimal{}
	if usdTarget, ok := targetUSD(usd, target); ok {
		for code, usdCode := range usd {
			rateMap[code] = usdCode.Div(usdTarget)
		}
	}

	incomes, err := s.st.Incomes(u.ID)
	if err != nil {
		return nil, err
	}
	cats, err := s.st.Categories(u.ID)
	if err != nil {
		return nil, err
	}
	expenses, err := s.st.Expenses(u.ID)
	if err != nil {
		return nil, err
	}

	in := plan.Input{Target: target, Rates: rateMap}
	for _, i := range incomes {
		in.Incomes = append(in.Incomes, plan.Income{
			ID: i.ID, Name: i.Name, Amount: mustDec(i.Amount), Currency: i.Currency, Period: i.Period,
		})
	}
	byCat := map[int64][]plan.Expense{}
	for _, e := range expenses {
		byCat[e.CategoryID] = append(byCat[e.CategoryID], plan.Expense{
			ID: e.ID, Name: e.Name, Amount: mustDec(e.Amount), Currency: e.Currency,
			Period: e.Period, Day: e.Day, Markup: mustDec(e.MarkupPercent),
		})
	}
	for _, c := range cats {
		in.Categories = append(in.Categories, plan.Category{
			ID: c.ID, Name: c.Name, Kind: c.Kind, Color: c.Color, Expenses: byCat[c.ID],
		})
	}
	out := plan.Compute(in)
	return &out, nil
}

func targetUSD(usd map[string]decimal.Decimal, target string) (decimal.Decimal, bool) {
	if target == "USD" {
		return decimal.NewFromInt(1), true
	}
	v, ok := usd[target]
	return v, ok
}

// mustDec: values are validated on write; a zero instead of a panic covers
// manual database edits.
func mustDec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

// ── DTO (snake_case, money as strings) ──────────────────────────────────────

type incomeDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Period      string `json:"period"`
	Monthly     string `json:"monthly"`
	Equivalent  string `json:"equivalent"`
	MissingRate bool   `json:"missing_rate,omitempty"`
}

type expenseDTO struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	Period        string `json:"period"`
	Day           *int   `json:"day"`
	MarkupPercent string `json:"markup_percent"`
	Monthly       string `json:"monthly"`
	Equivalent    string `json:"equivalent"`
	MissingRate   bool   `json:"missing_rate,omitempty"`
}

type categoryDTO struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Kind          string       `json:"kind"`
	Color         int          `json:"color"`
	Total         string       `json:"total"`
	ShareOfIncome *string      `json:"share_of_income"`
	Expenses      []expenseDTO `json:"expenses"`
}

type planDTO struct {
	TargetCurrency string        `json:"target_currency"`
	IncomeTotal    string        `json:"income_total"`
	ExpensesTotal  string        `json:"expenses_total"`
	ExpensesShare  *string       `json:"expenses_share"`
	SavingsTotal   string        `json:"savings_total"`
	SavingsShare   *string       `json:"savings_share"`
	Buffer         string        `json:"buffer"`
	BufferShare    *string       `json:"buffer_share"`
	MissingRates   []string      `json:"missing_rates"`
	Incomes        []incomeDTO   `json:"incomes"`
	Categories     []categoryDTO `json:"categories"`
}

func shareStr(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.StringFixed(4)
	return &s
}

func toDTO(sum *plan.Summary) planDTO {
	dto := planDTO{
		TargetCurrency: sum.Target,
		IncomeTotal:    sum.IncomeTotal.StringFixed(2),
		ExpensesTotal:  sum.ExpensesTotal.StringFixed(2),
		ExpensesShare:  shareStr(sum.ExpensesShare),
		SavingsTotal:   sum.SavingsTotal.StringFixed(2),
		SavingsShare:   shareStr(sum.SavingsShare),
		Buffer:         sum.Buffer.StringFixed(2),
		BufferShare:    shareStr(sum.BufferShare),
		MissingRates:   sum.MissingRates,
		Incomes:        []incomeDTO{},
		Categories:     []categoryDTO{},
	}
	for _, l := range sum.Incomes {
		dto.Incomes = append(dto.Incomes, incomeDTO{
			ID: l.ID, Name: l.Name, Amount: l.Amount.String(), Currency: l.Currency,
			Period: l.Period, Monthly: l.Monthly.String(),
			Equivalent: l.Equivalent.StringFixed(2), MissingRate: l.MissingRate,
		})
	}
	for _, c := range sum.Categories {
		cat := categoryDTO{
			ID: c.ID, Name: c.Name, Kind: c.Kind, Color: c.Color,
			Total: c.Total.StringFixed(2), ShareOfIncome: shareStr(c.ShareOfIncome),
			Expenses: []expenseDTO{},
		}
		for _, l := range c.Expenses {
			cat.Expenses = append(cat.Expenses, expenseDTO{
				ID: l.ID, Name: l.Name, Amount: l.Amount.String(), Currency: l.Currency,
				Period: l.Period, Day: l.Day, MarkupPercent: l.Markup.String(),
				Monthly: l.Monthly.String(), Equivalent: l.Equivalent.StringFixed(2),
				MissingRate: l.MissingRate,
			})
		}
		dto.Categories = append(dto.Categories, cat)
	}
	return dto
}
