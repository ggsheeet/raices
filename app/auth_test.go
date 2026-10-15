package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"raices/database"
)

func TestLoginWrongPasswordCreatesNoSession(t *testing.T) {
	mux, db := testMux(t, nil, time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC))
	rec := postLogin(t, mux, "admin@example.com", "not-the-password", "")
	assertNoSession(t, rec, db)
	if !strings.Contains(rec.Body.String(), "Correo o contraseña incorrectos.") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestLoginEmptyPasswordCreatesNoSession(t *testing.T) {
	mux, db := testMux(t, nil, time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC))
	rec := postLogin(t, mux, "admin@example.com", "", "")
	assertNoSession(t, rec, db)
}

func TestLoginSetsHttpOnlySecureLaxCookie(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, db := testMux(t, nil, now)
	rec := postLogin(t, mux, "admin@example.com", "admin-password", "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("Location = %q", loc)
	}
	c := sessionCookieFrom(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: HttpOnly=%v Secure=%v SameSite=%v", c.HttpOnly, c.Secure, c.SameSite)
	}
	if c.Path != "/" {
		t.Fatalf("Path = %q", c.Path)
	}
	if c.MaxAge != sessionMaxAge {
		t.Fatalf("MaxAge = %d", c.MaxAge)
	}
	sess, err := db.Queries.GetSession(context.Background(), hashToken(c.Value))
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.TokenHash == c.Value {
		t.Fatal("sessions.token_hash equals the cookie value")
	}
	if sess.TokenHash != hashToken(c.Value) {
		t.Fatalf("token_hash = %q", sess.TokenHash)
	}
}

func TestLoginRejectsUnsafeNext(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, _ := testMux(t, nil, now)
	for _, next := range []string{"https://evil.example", "//evil.example"} {
		rec := postLogin(t, mux, "admin@example.com", "admin-password", next)
		if loc := rec.Header().Get("Location"); loc != "/admin" {
			t.Fatalf("next %q Location = %q", next, loc)
		}
	}
}

func TestLoginHonorsSafeNext(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, _ := testMux(t, nil, now)
	rec := postLogin(t, mux, "admin@example.com", "admin-password", "/tickets/claim/abc")
	if loc := rec.Header().Get("Location"); loc != "/tickets/claim/abc" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestLoginPagePreservesNext(t *testing.T) {
	mux, _ := testMux(t, nil, time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC))
	req := httptest.NewRequest(http.MethodGet, "/login?next=/tickets/claim/abc", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `name="next"`) || !strings.Contains(body, "/tickets/claim/abc") {
		t.Fatalf("missing next field: %s", body)
	}
}

func TestAdminRequiresSession(t *testing.T) {
	mux, _ := testMux(t, nil, time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC))
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login?next=/admin" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestAdminWithSession(t *testing.T) {
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	mux, _ := testMux(t, nil, now)
	c := loginCookie(t, mux)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Atención") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func postLogin(t *testing.T, mux http.Handler, email, password, next string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"email": {email}, "password": {password}, "next": {next}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("missing session cookie")
	return nil
}

func loginCookie(t *testing.T, mux http.Handler) *http.Cookie {
	t.Helper()
	return sessionCookieFrom(t, postLogin(t, mux, "admin@example.com", "admin-password", ""))
}

func assertNoSession(t *testing.T, rec *httptest.ResponseRecorder, db *database.DB) {
	t.Helper()
	res := rec.Result()
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			t.Fatal("unexpected session cookie")
		}
	}
	n, err := db.Queries.CountSessions(context.Background())
	if err != nil {
		t.Fatalf("CountSessions: %v", err)
	}
	if n != 0 {
		t.Fatalf("sessions = %d", n)
	}
}
