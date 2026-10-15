package app

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
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
