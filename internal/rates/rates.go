// Package rates — the global USD-based exchange-rate cache.
// Stored rate = how many USD one unit of a currency costs. A single fetch of
// latest/USD covers every currency for every user regardless of their target;
// per-user conversion is a cross rate: rate(code→target) = usd(code)/usd(target).
package rates

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/vesmirov/finance-api/internal/store"
)

var one = decimal.NewFromInt(1)

// ERAPI — open.er-api.com, keyless. The only provider.
type ERAPI struct {
	BaseURL string // for tests; defaults to https://open.er-api.com
	Client  *http.Client
}

// Fetch returns code → USD per 1 unit for every currency in the response.
func (p *ERAPI) Fetch() (map[string]decimal.Decimal, error) {
	base := p.BaseURL
	if base == "" {
		base = "https://open.er-api.com"
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Get(base + "/v6/latest/USD")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// json.Number keeps rates out of float64
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var body struct {
		Result string                 `json:"result"`
		Rates  map[string]json.Number `json:"rates"`
	}
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	if body.Result != "success" {
		return nil, fmt.Errorf("open.er-api returned %q", body.Result)
	}
	// The response is "how many X per 1 USD"; we store the inverse.
	out := make(map[string]decimal.Decimal, len(body.Rates))
	for code, num := range body.Rates {
		v, err := decimal.NewFromString(num.String())
		if err != nil || v.Sign() <= 0 {
			continue
		}
		out[code] = one.Div(v)
	}
	return out, nil
}

// Refresh updates the cache for every currency in the provider response, so a
// user can start tracking any currency and get a rate immediately.
func Refresh(st *store.Store, p *ERAPI) error {
	fetched, err := p.Fetch()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for code, rate := range fetched {
		if err := st.UpsertRate(code, rate.String(), now); err != nil {
			return err
		}
	}
	return nil
}

// Runner — background auto-refresh (FINANCE_RATES_REFRESH, default 5m, 0 = off).
// A network error is a warning; the app keeps working on the cache.
func Runner(st *store.Store, p *ERAPI, interval time.Duration, log *slog.Logger, stop <-chan struct{}) {
	if interval <= 0 {
		return
	}
	run := func() {
		if err := Refresh(st, p); err != nil {
			log.Warn("rates refresh failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			run()
		case <-stop:
			return
		}
	}
}
