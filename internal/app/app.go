package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"go-education/internal/clients"
	"go-education/internal/docs"
	"go-education/internal/employees"
	"go-education/internal/health"
	"go-education/internal/middleware"
	"go-education/internal/records"
	"go-education/migrations"
)

type Config struct {
	DatabaseURL   string
	MigrationsURL string
	Location      *time.Location
	Logger        *slog.Logger
}

// ConfigFromEnv читает DATABASE_URL, DATABASE_URL_UNPOOLED и APP_TZ.
func ConfigFromEnv(logger *slog.Logger) (Config, error) {
	loc := time.Local
	if tz := os.Getenv("APP_TZ"); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil {
			return Config{}, err
		}
	}
	return Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		MigrationsURL: os.Getenv("DATABASE_URL_UNPOOLED"),
		Location:      loc,
		Logger:        logger,
	}, nil
}

// New применяет миграции, подключается к БД и возвращает готовый обработчик.
// Вызывающий обязан закрыть пул, вызвав close.
func New(ctx context.Context, cfg Config) (h http.Handler, close func(), err error) {
	if cfg.DatabaseURL == "" {
		return nil, nil, errMissingDB
	}
	migrationsURL := cfg.MigrationsURL
	if migrationsURL == "" {
		migrationsURL = cfg.DatabaseURL
	}
	if err := migrations.Up(migrationsURL); err != nil {
		return nil, nil, err
	}
	cfg.Logger.Info("миграции применены", "tz", cfg.Location.String())

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}

	mux := http.NewServeMux()
	records.NewHandler(records.NewStorage(db), cfg.Location).Register(mux)
	clients.NewHandler(clients.NewStorage(db)).Register(mux)
	employees.NewHandler(employees.NewStorage(db)).Register(mux)
	mux.HandleFunc("GET /healthz", health.Handler(db))
	docs.Register(mux)

	h = middleware.Chain(mux,
		middleware.Logging(cfg.Logger),
		middleware.Recover(cfg.Logger),
	)
	return h, db.Close, nil
}

var errMissingDB = errors.New("не задана переменная окружения DATABASE_URL")

// NewLogger: LOG_LEVEL=debug|info|warn|error (по умолчанию info),
// LOG_FORMAT=json для машинного формата (по умолчанию текст).
func NewLogger() *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if os.Getenv("LOG_FORMAT") == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
