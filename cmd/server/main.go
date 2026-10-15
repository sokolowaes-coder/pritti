package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // база часовых поясов внутри бинарника: в distroless-образе её нет

	"go-education/internal/app"
	"go-education/internal/health"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// `server healthcheck` — проверка для Docker: в образе нет curl.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := health.Check("http://localhost:" + port + "/healthz"); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}

	logger := app.NewLogger()
	slog.SetDefault(logger)

	if err := run(port, logger); err != nil {
		logger.Error("сервер завершился с ошибкой", "error", err)
		os.Exit(1)
	}
}

func run(port string, logger *slog.Logger) error {
	// ctx отменяется по Ctrl+C (SIGINT) или docker stop (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := app.ConfigFromEnv(logger)
	if err != nil {
		return err
	}
	handler, closeDB, err := app.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeDB()

	// Таймауты не дают медленным или зависшим клиентам бесконечно держать соединения.
	// WriteTimeout больше, чем таймаут обработки запроса в app (10 с).
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("сервер запущен", "addr", "http://localhost:"+port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}
	logger.Info("останавливаю сервер")

	// Даём текущим запросам до 10 секунд на завершение.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("сервер остановлен")
	return nil
}
