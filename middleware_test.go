package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLogRedactsClaimToken(t *testing.T) {
	var buf bytes.Buffer
	orig := logger
	logger = NewCustomLogger("", &buf)
	t.Cleanup(func() { logger = orig })

	token := "super-secret-claim-token"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets/claim/{token}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := requestLog(mux)

	req := httptest.NewRequest(http.MethodGet, "/tickets/claim/"+token, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	line := buf.String()
	if !strings.Contains(line, "/tickets/claim/") {
		t.Fatalf("log missing claim prefix: %q", line)
	}
	if strings.Contains(line, token) {
		t.Fatalf("log contains token: %q", line)
	}

	buf.Reset()
	loginReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	handler.ServeHTTP(httptest.NewRecorder(), loginReq)
	loginLine := buf.String()
	if !strings.Contains(loginLine, "/login") {
		t.Fatalf("other path changed: %q", loginLine)
	}
}
