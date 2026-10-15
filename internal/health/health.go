// Package health — эндпоинт проверки, что сервер жив и видит базу.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"pritti/internal/httpx"
)

// Pinger — всё, что нужно от БД для проверки. *pgxpool.Pool подходит.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler отвечает 200 {"status":"ok"}, если БД отвечает за 2 секунды,
// иначе 503 {"status":"unavailable"}.
func Handler(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status, body := http.StatusOK, "ok"
		if err := db.Ping(ctx); err != nil {
			httpx.Logger(r.Context()).Warn("healthcheck: БД недоступна", "error", err)
			status, body = http.StatusServiceUnavailable, "unavailable"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]string{"status": body})
	}
}

// Check делает GET на /healthz и возвращает nil при ответе 200.
// Используется командой `server healthcheck` в Docker: в образе нет curl.
func Check(url string) error {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz ответил %d", resp.StatusCode)
	}
	return nil
}
