package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pritti/internal/clients"
	"pritti/internal/docs"
	"pritti/internal/employees"
	"pritti/internal/health"
	"pritti/internal/middleware"
	"pritti/internal/records"
	"pritti/migrations"
)

type Config struct {
	DatabaseURL   string
	MigrationsURL string
	APIKey        string // пусто — API без авторизации
	Location      *time.Location
	Logger        *slog.Logger
}

// requestTimeout — сколько максимум обрабатывается один запрос, включая запросы к БД.
const requestTimeout = 10 * time.Second

// publicPaths доступны без API-ключа: проверка здоровья и документация.
var publicPaths = []string{"/healthz", "/swagger", "/openapi.yaml"}

// ConfigFromEnv читает DATABASE_URL, DATABASE_URL_UNPOOLED, API_KEY и APP_TZ.
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
		APIKey:        os.Getenv("API_KEY"),
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
	if cfg.APIKey == "" {
		cfg.Logger.Warn("API_KEY не задан — API доступен без авторизации")
	}

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}

	return NewHandler(db, cfg), db.Close, nil
}

// NewHandler собирает роуты и middleware поверх готового пула БД.
// Отдельно от New, чтобы тесты могли передать свою БД без миграций и переменных окружения.
func NewHandler(db *pgxpool.Pool, cfg Config) http.Handler {
	mux := http.NewServeMux()
	records.NewHandler(records.NewStorage(db), cfg.Location).Register(mux)
	clients.NewHandler(clients.NewStorage(db)).Register(mux)
	employees.NewHandler(employees.NewStorage(db)).Register(mux)
	mux.HandleFunc("GET /healthz", health.Handler(db))
	docs.Register(mux)

	return middleware.Chain(mux,
		middleware.Logging(cfg.Logger),
		middleware.Recover(cfg.Logger),
		middleware.APIKey(cfg.APIKey, publicPaths...),
		middleware.Timeout(requestTimeout),
	)
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
