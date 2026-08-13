package server

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/vesmirov/finance-api/internal/store"
)

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// ── validation ──────────────────────────────────────────────────────────────

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func validAmount(w http.ResponseWriter, s string) bool {
	d, err := decimal.NewFromString(s)
	if err != nil || d.Sign() < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "amount must be a non-negative decimal string")
		return false
	}
	return true
}

func validMarkup(w http.ResponseWriter, s string) bool {
	d, err := decimal.NewFromString(s)
	if err != nil || d.Sign() < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "markup_percent must be a non-negative decimal string")
		return false
	}
	return true
}

func validPeriod(w http.ResponseWriter, s string) bool {
	if s != "monthly" && s != "annual" {
		writeErr(w, http.StatusUnprocessableEntity, "period must be monthly or annual")
		return false
	}
	return true
}

// validCurrency: the code must be tracked by the user. The target currency is
// always implicitly tracked — a brand-new user starts with an empty tracked
// list (no seed data exists) and must still be able to add rows in the target.
func (s *Server) validCurrency(w http.ResponseWriter, u *store.User, code string) bool {
	if !currencyRe.MatchString(code) {
		writeErr(w, http.StatusUnprocessableEntity, "currency must be a 3-letter code")
		return false
	}
	if code == u.TargetCurrency {
		return true
	}
	tracked, err := s.st.UserCurrencies(u.ID)
	if err != nil {
		s.storeErr(w, err)
		return false
	}
	for _, c := range tracked {
		if c == code {
			return true
		}
	}
	writeErr(w, http.StatusUnprocessableEntity, "currency is not tracked: "+code)
	return false
}

func validDay(w http.ResponseWriter, day *int) bool {
	if day != nil && (*day < 1 || *day > 31) {
		writeErr(w, http.StatusUnprocessableEntity, "day must be within 1..31")
		return false
	}
	return true
}

// ── health / plan ───────────────────────────────────────────────────────────

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	var fetchedAt *string
	if rs, err := s.st.Rates(); err == nil && len(rs) > 0 {
		oldest := rs[0].FetchedAt
		for _, r := range rs[1:] {
			if r.FetchedAt < oldest {
				oldest = r.FetchedAt
			}
		}
		fetchedAt = &oldest
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": s.version, "rates_fetched_at": fetchedAt,
	})
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	sum, err := s.buildPlan(s.user(r))
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(sum))
}

// ── incomes ─────────────────────────────────────────────────────────────────

type incomeBody struct {
	Name     *string `json:"name"`
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
	Period   *string `json:"period"`
	Position *int    `json:"position"`
}

func (s *Server) createIncome(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	var b incomeBody
	if !decodeBody(w, r, &b) {
		return
	}
	inc := store.Income{UserID: u.ID, Name: "", Amount: "0", Currency: u.TargetCurrency, Period: "monthly"}
	if b.Name != nil {
		inc.Name = *b.Name
	}
	if b.Amount != nil {
		if !validAmount(w, *b.Amount) {
			return
		}
		inc.Amount = *b.Amount
	}
	if b.Currency != nil {
		if !s.validCurrency(w, u, *b.Currency) {
			return
		}
		inc.Currency = *b.Currency
	}
	if b.Period != nil {
		if !validPeriod(w, *b.Period) {
			return
		}
		inc.Period = *b.Period
	}
	if b.Position != nil {
		inc.Position = *b.Position
	} else if existing, err := s.st.Incomes(u.ID); err == nil {
		inc.Position = len(existing)
	}
	id, err := s.st.InsertIncome(inc)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": inc.Name, "amount": inc.Amount,
		"currency": inc.Currency, "period": inc.Period, "position": inc.Position,
	})
}

func (s *Server) patchIncome(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b incomeBody
	if !decodeBody(w, r, &b) {
		return
	}
	fields := map[string]any{}
	if b.Name != nil {
		fields["name"] = *b.Name
	}
	if b.Amount != nil {
		if !validAmount(w, *b.Amount) {
			return
		}
		fields["amount"] = *b.Amount
	}
	if b.Currency != nil {
		if !s.validCurrency(w, u, *b.Currency) {
			return
		}
		fields["currency"] = *b.Currency
	}
	if b.Period != nil {
		if !validPeriod(w, *b.Period) {
			return
		}
		fields["period"] = *b.Period
	}
	if b.Position != nil {
		fields["position"] = *b.Position
	}
	if err := s.st.UpdateIncome(u.ID, id, fields); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteIncome(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteIncome(u.ID, id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── categories ──────────────────────────────────────────────────────────────

type categoryBody struct {
	Name     *string `json:"name"`
	Kind     *string `json:"kind"`
	Color    *int    `json:"color"`
	Position *int    `json:"position"`
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.st.Categories(s.user(r).ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		out = append(out, map[string]any{
			"id": c.ID, "name": c.Name, "kind": c.Kind, "color": c.Color, "position": c.Position,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) freeColor(userID int64) (int, bool, error) {
	cats, err := s.st.Categories(userID)
	if err != nil {
		return 0, false, err
	}
	used := map[int]bool{}
	for _, c := range cats {
		used[c.Color] = true
	}
	for slot := 1; slot <= 8; slot++ {
		if !used[slot] {
			return slot, true, nil
		}
	}
	return 0, false, nil
}

func (s *Server) colorTaken(userID int64, color int, exclude int64) (bool, error) {
	cats, err := s.st.Categories(userID)
	if err != nil {
		return false, err
	}
	for _, c := range cats {
		if c.Color == color && c.ID != exclude {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	var b categoryBody
	if !decodeBody(w, r, &b) {
		return
	}
	if b.Kind == nil || (*b.Kind != "expense" && *b.Kind != "saving") {
		writeErr(w, http.StatusUnprocessableEntity, "kind must be expense or saving")
		return
	}
	cat := store.Category{UserID: u.ID, Kind: *b.Kind}
	if b.Name != nil {
		cat.Name = *b.Name
	} else if cat.Kind == "saving" {
		cat.Name = "New savings"
	} else {
		cat.Name = "New category"
	}
	if b.Color != nil {
		if *b.Color < 1 || *b.Color > 8 {
			writeErr(w, http.StatusUnprocessableEntity, "color must be a palette slot 1..8")
			return
		}
		taken, err := s.colorTaken(u.ID, *b.Color, 0)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if taken {
			writeErr(w, http.StatusConflict, "color slot is already taken")
			return
		}
		cat.Color = *b.Color
	} else {
		slot, ok, err := s.freeColor(u.ID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusConflict, "all 8 color slots are taken")
			return
		}
		cat.Color = slot
	}
	if b.Position != nil {
		cat.Position = *b.Position
	} else if cats, err := s.st.Categories(u.ID); err == nil {
		cat.Position = len(cats)
	}
	id, err := s.st.InsertCategory(cat)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "name": cat.Name, "kind": cat.Kind, "color": cat.Color, "position": cat.Position,
	})
}

func (s *Server) patchCategory(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b categoryBody
	if !decodeBody(w, r, &b) {
		return
	}
	if b.Kind != nil {
		writeErr(w, http.StatusUnprocessableEntity, "kind is immutable")
		return
	}
	fields := map[string]any{}
	if b.Name != nil {
		fields["name"] = *b.Name
	}
	if b.Color != nil {
		if *b.Color < 1 || *b.Color > 8 {
			writeErr(w, http.StatusUnprocessableEntity, "color must be a palette slot 1..8")
			return
		}
		taken, err := s.colorTaken(u.ID, *b.Color, id)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if taken {
			writeErr(w, http.StatusConflict, "color slot is already taken")
			return
		}
		fields["color"] = *b.Color
	}
	if b.Position != nil {
		fields["position"] = *b.Position
	}
	if err := s.st.UpdateCategory(u.ID, id, fields); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteCategory(u.ID, id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── expenses ────────────────────────────────────────────────────────────────

type expenseBody struct {
	CategoryID    *int64  `json:"category_id"`
	Name          *string `json:"name"`
	Amount        *string `json:"amount"`
	Currency      *string `json:"currency"`
	Period        *string `json:"period"`
	Day           *int    `json:"day"`
	MarkupPercent *string `json:"markup_percent"`
	Position      *int    `json:"position"`
}

func (s *Server) createExpense(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	var b expenseBody
	if !decodeBody(w, r, &b) {
		return
	}
	if b.CategoryID == nil {
		writeErr(w, http.StatusUnprocessableEntity, "category_id is required")
		return
	}
	e := store.Expense{CategoryID: *b.CategoryID, Amount: "0", Currency: u.TargetCurrency,
		Period: "monthly", MarkupPercent: "0"}
	if b.Name != nil {
		e.Name = *b.Name
	}
	if b.Amount != nil {
		if !validAmount(w, *b.Amount) {
			return
		}
		e.Amount = *b.Amount
	}
	if b.Currency != nil {
		if !s.validCurrency(w, u, *b.Currency) {
			return
		}
		e.Currency = *b.Currency
	}
	if b.Period != nil {
		if !validPeriod(w, *b.Period) {
			return
		}
		e.Period = *b.Period
	}
	if !validDay(w, b.Day) {
		return
	}
	e.Day = b.Day
	if b.MarkupPercent != nil {
		if !validMarkup(w, *b.MarkupPercent) {
			return
		}
		e.MarkupPercent = *b.MarkupPercent
	}
	if b.Position != nil {
		e.Position = *b.Position
	} else if all, err := s.st.Expenses(u.ID); err == nil {
		n := 0
		for _, x := range all {
			if x.CategoryID == e.CategoryID {
				n++
			}
		}
		e.Position = n
	}
	id, err := s.st.InsertExpense(u.ID, e)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusUnprocessableEntity, "category does not exist")
			return
		}
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "category_id": e.CategoryID, "name": e.Name, "amount": e.Amount,
		"currency": e.Currency, "period": e.Period, "day": e.Day,
		"markup_percent": e.MarkupPercent, "position": e.Position,
	})
}

func (s *Server) patchExpense(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b expenseBody
	if !decodeBody(w, r, &b) {
		return
	}
	fields := map[string]any{}
	if b.CategoryID != nil {
		// the target category must belong to the same user
		taken := false
		cats, err := s.st.Categories(u.ID)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		for _, c := range cats {
			if c.ID == *b.CategoryID {
				taken = true
				break
			}
		}
		if !taken {
			writeErr(w, http.StatusUnprocessableEntity, "category does not exist")
			return
		}
		fields["category_id"] = *b.CategoryID
	}
	if b.Name != nil {
		fields["name"] = *b.Name
	}
	if b.Amount != nil {
		if !validAmount(w, *b.Amount) {
			return
		}
		fields["amount"] = *b.Amount
	}
	if b.Currency != nil {
		if !s.validCurrency(w, u, *b.Currency) {
			return
		}
		fields["currency"] = *b.Currency
	}
	if b.Period != nil {
		if !validPeriod(w, *b.Period) {
			return
		}
		fields["period"] = *b.Period
	}
	if b.Day != nil {
		if !validDay(w, b.Day) {
			return
		}
		fields["day"] = *b.Day
	}
	if b.MarkupPercent != nil {
		if !validMarkup(w, *b.MarkupPercent) {
			return
		}
		fields["markup_percent"] = *b.MarkupPercent
	}
	if b.Position != nil {
		fields["position"] = *b.Position
	}
	if err := s.st.UpdateExpense(u.ID, id, fields); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteExpense(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteExpense(u.ID, id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── per-user settings & currencies ──────────────────────────────────────────

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"target_currency": s.user(r).TargetCurrency})
}

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	var b struct {
		TargetCurrency *string `json:"target_currency"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if b.TargetCurrency != nil {
		code := *b.TargetCurrency
		if !currencyRe.MatchString(code) {
			writeErr(w, http.StatusUnprocessableEntity, "target_currency must be a 3-letter code")
			return
		}
		// the target is always tracked
		if err := s.st.AddUserCurrency(u.ID, code); err != nil {
			s.storeErr(w, err)
			return
		}
		if err := s.st.UpdateUser(u.ID, map[string]any{"target_currency": code}); err != nil {
			s.storeErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listCurrencies(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	tracked, err := s.st.UserCurrencies(u.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	// the target is always present, even for a brand-new user with an empty list
	hasTarget := false
	for _, c := range tracked {
		if c == u.TargetCurrency {
			hasTarget = true
			break
		}
	}
	if !hasTarget {
		tracked = append([]string{u.TargetCurrency}, tracked...)
	}
	writeJSON(w, http.StatusOK, tracked)
}

func (s *Server) addCurrency(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	var b struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	code := strings.ToUpper(strings.TrimSpace(b.Code))
	if !currencyRe.MatchString(code) {
		writeErr(w, http.StatusUnprocessableEntity, "code must be a 3-letter currency code")
		return
	}
	if err := s.st.AddUserCurrency(u.ID, code); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"code": code})
}

func (s *Server) removeCurrency(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	code := r.PathValue("code")
	if code == u.TargetCurrency {
		writeErr(w, http.StatusConflict, "cannot remove the target currency")
		return
	}
	used, err := s.st.CurrencyReferenced(u.ID, code)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if used {
		writeErr(w, http.StatusConflict, "currency is used by existing rows")
		return
	}
	if err := s.st.RemoveUserCurrency(u.ID, code); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getRates — a read-only, target-relative view of the global cache for the
// user's tracked currencies. Rates refresh automatically; there is no manual
// refresh endpoint.
func (s *Server) getRates(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	stored, err := s.st.Rates()
	if err != nil {
		s.storeErr(w, err)
		return
	}
	usd := map[string]decimal.Decimal{}
	fetchedAt := ""
	for _, rt := range stored {
		if d, err := decimal.NewFromString(rt.RateUSD); err == nil && d.Sign() > 0 {
			usd[rt.Code] = d
		}
		if rt.FetchedAt > fetchedAt {
			fetchedAt = rt.FetchedAt
		}
	}
	tracked, err := s.st.UserCurrencies(u.ID)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	list := []map[string]string{}
	if usdTarget, ok := targetUSD(usd, u.TargetCurrency); ok {
		for _, code := range tracked {
			if code == u.TargetCurrency {
				continue
			}
			if usdCode, ok := usd[code]; ok {
				list = append(list, map[string]string{
					"code": code, "rate": usdCode.Div(usdTarget).Round(6).String(),
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"target": u.TargetCurrency, "fetched_at": fetchedAt, "rates": list,
	})
}
