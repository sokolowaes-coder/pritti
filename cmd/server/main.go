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

	"pritti/internal/app"
	"pritti/internal/health"
	"pritti/migrations"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger := app.NewLogger()
	slog.SetDefault(logger)

	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var err error
	switch command {
	case "":
		err = run(port, logger)
	case "healthcheck":
		// `server healthcheck` — проверка для Docker: в образе нет curl.
		err = health.Check("http://localhost:" + port + "/healthz")
	case "migrate":
		err = migrate(logger)
	default:
		err = errors.New("неизвестная команда " + command + " (есть: healthcheck, migrate)")
	}
	if err != nil {
		logger.Error("ошибка", "command", command, "error", err)
		os.Exit(1)
	}
}

// migrate — `server migrate`: применяет миграции и выходит. Отдельным шагом, а не при
// каждом старте сервера: изменение схемы на проде видно и запускается осознанно.
// Нужна только DATABASE_URL_UNPOOLED или DATABASE_URL — остальные настройки сервера не нужны.
func migrate(logger *slog.Logger) error {
	url := os.Getenv("DATABASE_URL_UNPOOLED")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		return errors.New("не задана DATABASE_URL_UNPOOLED или DATABASE_URL")
	}
	if err := migrations.Up(url); err != nil {
		return err
	}
	version, _ := migrations.Latest()
	logger.Info("миграции применены", "version", version)
	return nil
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
