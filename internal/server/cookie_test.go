package server

import (
	"bytes"
	"net/http"
	"testing"
)

// The session cookie is Secure only when the request came in over TLS,
// directly or through a proxy that forwards X-Forwarded-Proto.
func TestSessionCookieSecureFlag(t *testing.T) {
	ts, _ := testServer(t)
	client := &http.Client{} // no jar: every request is a fresh sign-in

	login := func(forwardedProto string) *http.Cookie {
		t.Helper()
		body := []byte(`{"login":"tester","password":"secret-pass"}`)
		req, err := http.NewRequest("POST", ts.URL+"/api/auth/login", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if forwardedProto != "" {
			req.Header.Set("X-Forwarded-Proto", forwardedProto)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("login: %d", resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatal("no session cookie in response")
		return nil
	}

	if c := login(""); c.Secure {
		t.Error("plain http: cookie must not be Secure")
	}
	if c := login("http"); c.Secure {
		t.Error("X-Forwarded-Proto http: cookie must not be Secure")
	}
	if c := login("https"); !c.Secure {
		t.Error("X-Forwarded-Proto https: cookie must be Secure")
	}
}
