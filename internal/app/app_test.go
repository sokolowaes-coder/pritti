package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pritti/internal/middleware"
	"pritti/internal/testdb"
)

// Проверяет приложение целиком: роуты всех пакетов подключены,
// а API-ключ требуется везде, кроме публичных путей.
func TestHandlerRoutesAndAPIKey(t *testing.T) {
	h := NewHandler(testdb.New(t), Config{
		APIKey:   "secret",
		Location: time.UTC,
		Logger:   slog.New(slog.DiscardHandler),
	})

	tests := []struct {
		name       string
		method     string
		path       string
		key        string
		wantStatus int
	}{
		{"healthz без ключа", "GET", "/healthz", "", http.StatusOK},
		{"swagger без ключа", "GET", "/swagger", "", http.StatusOK},
		{"openapi без ключа", "GET", "/openapi.yaml", "", http.StatusOK},

		{"записи без ключа", "GET", "/records", "", http.StatusUnauthorized},
		{"клиенты без ключа", "GET", "/clients", "", http.StatusUnauthorized},
		{"сотрудники без ключа", "GET", "/employees", "", http.StatusUnauthorized},
		{"неверный ключ", "GET", "/clients", "wrong", http.StatusUnauthorized},

		{"записи с ключом", "GET", "/records", "secret", http.StatusOK},
		{"клиенты с ключом", "GET", "/clients", "secret", http.StatusOK},
		{"сотрудники с ключом", "GET", "/employees", "secret", http.StatusOK},
		{"клиент по id", "GET", "/clients/00000000-0000-0000-0000-000000000999", "secret", http.StatusNotFound},
		{"сотрудник по id", "GET", "/employees/00000000-0000-0000-0000-000000000999", "secret", http.StatusNotFound},
		{"неизвестный путь", "GET", "/nope", "secret", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.key != "" {
				req.Header.Set(middleware.APIKeyHeader, tt.key)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("статус = %d, ожидали %d; тело: %s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestHandlerSecurityAndRateLimit(t *testing.T) {
	h := NewHandler(testdb.New(t), Config{
		APIKey:         "secret",
		RateLimitRPS:   1,
		RateLimitBurst: 2,
		RealIPHeader:   "X-Real-IP",
		Location:       time.UTC,
		Logger:         slog.New(slog.DiscardHandler),
	})
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/clients", nil)
		req.Header.Set("X-Real-IP", "203.0.113.7")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Без ключа: лимит стоит раньше проверки ключа, так что перебор ключа тоже ограничен.
	for i, want := range []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusTooManyRequests} {
		rec := call()
		if rec.Code != want {
			t.Errorf("запрос %d: статус %d, ожидали %d", i+1, rec.Code, want)
		}
		if rec.Header().Get(middleware.RequestIDHeader) == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("запрос %d: нет X-Request-ID или защитных заголовков: %v", i+1, rec.Header())
		}
	}
}

func TestConfigFromEnv(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	base := map[string]string{"DATABASE_URL": "postgres://x", "API_KEY": "key"}

	tests := []struct {
		name    string
		env     map[string]string // поверх base; "" — переменная не задана
		wantErr string
		check   func(t *testing.T, cfg Config)
	}{
		{name: "по умолчанию", check: func(t *testing.T, cfg Config) {
			if cfg.APIKey != "key" || cfg.MigrateOnStart || cfg.RateLimitRPS != 20 || cfg.RateLimitBurst != 40 || cfg.RealIPHeader != "" {
				t.Errorf("%+v", cfg)
			}
		}},
		{name: "без DATABASE_URL", env: map[string]string{"DATABASE_URL": ""}, wantErr: "DATABASE_URL"},
		{name: "без API_KEY сервер не стартует", env: map[string]string{"API_KEY": ""}, wantErr: "API_KEY"},
		{name: "AUTH_DISABLED выключает ключ", env: map[string]string{"API_KEY": "", "AUTH_DISABLED": "true"}, check: func(t *testing.T, cfg Config) {
			if cfg.APIKey != "" {
				t.Errorf("APIKey = %q", cfg.APIKey)
			}
		}},
		{name: "кривой AUTH_DISABLED", env: map[string]string{"AUTH_DISABLED": "да"}, wantErr: "AUTH_DISABLED"},
		{name: "MIGRATE_ON_START", env: map[string]string{"MIGRATE_ON_START": "1"}, check: func(t *testing.T, cfg Config) {
			if !cfg.MigrateOnStart {
				t.Error("MigrateOnStart = false")
			}
		}},
		{name: "лимит выключается нулём", env: map[string]string{"RATE_LIMIT_RPS": "0"}, check: func(t *testing.T, cfg Config) {
			if cfg.RateLimitRPS != 0 {
				t.Errorf("RateLimitRPS = %v", cfg.RateLimitRPS)
			}
		}},
		{name: "кривой лимит", env: map[string]string{"RATE_LIMIT_RPS": "-1"}, wantErr: "RATE_LIMIT_RPS"},
		{name: "на Vercel IP из X-Real-IP", env: map[string]string{"VERCEL": "1"}, check: func(t *testing.T, cfg Config) {
			if cfg.RealIPHeader != "X-Real-IP" {
				t.Errorf("RealIPHeader = %q", cfg.RealIPHeader)
			}
		}},
		{name: "кривой APP_TZ", env: map[string]string{"APP_TZ": "Mars/Base"}, wantErr: "APP_TZ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"DATABASE_URL", "DATABASE_URL_UNPOOLED", "API_KEY", "AUTH_DISABLED", "MIGRATE_ON_START",
				"RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "REAL_IP_HEADER", "VERCEL", "APP_TZ"} {
				t.Setenv(k, base[k])
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := ConfigFromEnv(logger)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ошибка = %v, ожидали про %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, cfg)
		})
	}
}
