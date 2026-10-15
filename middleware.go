package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"raices/env"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

func SecurityMiddleware(next http.Handler) http.Handler {
	handler := contentType(next)
	handler = maxBytes(handler)
	handler = http.TimeoutHandler(handler, 30*time.Second, "request timeout\n")
	handler = cors(handler)
	handler = securityHeaders(handler)
	handler = rateLimit(handler)
	handler = requestLog(handler)
	handler = requestID(handler)
	return handler
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := safeRequestID(r.Header.Get("X-Request-ID"))
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/public/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		path := r.URL.Path
		if strings.HasPrefix(path, "/tickets/claim/") {
			path = "/tickets/claim/"
		}
		logger.Info("%s %s %d %s", r.Method, path, status, time.Since(start))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status != 0 {
		return
	}
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://sdk.mercadopago.com; style-src 'self'; img-src 'self' data: https://http2.mlstatic.com; font-src 'self'; connect-src 'self' https://api.mercadopago.com https://secure-fields.mercadopago.com https://api-static.mercadopago.com; frame-src https://secure-fields.mercadopago.com https://api-static.mercadopago.com; form-action 'self'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && strings.TrimRight(origin, "/") == strings.TrimRight(env.AppBaseURL, "/") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, HX-Request, HX-Target, HX-Trigger, HX-Current-URL, HX-Boosted")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func maxBytes(next http.Handler) http.Handler {
	const limit = 1 << 20
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func contentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			next.ServeHTTP(w, r)
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		switch {
		case err == nil && (media == "application/x-www-form-urlencoded" || media == "multipart/form-data" || media == "application/json"):
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
		}
	})
}

var requestLimiter = newLimiter(20, 40)

func rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requestLimiter.allow(clientIP(r)) {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type visitor struct {
	tokens float64
	seen   time.Time
}

type limiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     float64
	burst    float64
}

func newLimiter(perSecond, burst float64) *limiter {
	return &limiter{
		visitors: map[string]*visitor{},
		rate:     perSecond,
		burst:    burst,
	}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.visitors) > 1024 {
		for k, v := range l.visitors {
			if now.Sub(v.seen) > 10*time.Minute {
				delete(l.visitors, k)
			}
		}
	}

	v, ok := l.visitors[key]
	if !ok {
		l.visitors[key] = &visitor{tokens: l.burst - 1, seen: now}
		return true
	}

	elapsed := now.Sub(v.seen).Seconds()
	v.seen = now
	v.tokens += elapsed * l.rate
	if v.tokens > l.burst {
		v.tokens = l.burst
	}
	if v.tokens < 1 {
		return false
	}
	v.tokens--
	return true
}

func safeRequestID(value string) string {
	if value == "" || len(value) > 64 {
		return newRequestID()
	}
	for _, c := range value {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return newRequestID()
		}
	}
	return value
}

func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}
