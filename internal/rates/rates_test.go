package rates

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vesmirov/finance-api/internal/testdb"
)

func TestERAPIFetchInvertsUSDBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v6/latest/USD" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"result":"success","rates":{"USD":1,"EUR":0.86633,"THB":38.3}}`))
	}))
	defer srv.Close()

	got, err := (&ERAPI{BaseURL: srv.URL}).Fetch()
	if err != nil {
		t.Fatal(err)
	}
	// 1/0.86633 ≈ 1.1543 USD per 1 EUR
	if got["EUR"].Round(4).String() != "1.1543" {
		t.Errorf("EUR = %s, want ~1.1543", got["EUR"])
	}
	if got["THB"].Round(4).String() != "0.0261" {
		t.Errorf("THB = %s, want ~0.0261", got["THB"])
	}
	if got["USD"].String() != "1" {
		t.Errorf("USD = %s, want 1", got["USD"])
	}
}

func TestERAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"result":"error"}`))
	}))
	defer srv.Close()
	if _, err := (&ERAPI{BaseURL: srv.URL}).Fetch(); err != nil {
		return
	}
	t.Fatal("want error on non-success result")
}

func TestRefreshStoresEveryCode(t *testing.T) {
	st := testdb.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"result":"success","rates":{"USD":1,"EUR":0.86633,"GEL":2.62,"KZT":533.1}}`))
	}))
	defer srv.Close()

	if err := Refresh(st, &ERAPI{BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	rs, err := st.Rates()
	if err != nil {
		t.Fatal(err)
	}
	// the whole response is cached, so any user can start tracking any
	// currency and get a rate immediately
	if len(rs) != 4 {
		t.Fatalf("stored %d rates, want 4", len(rs))
	}
}
