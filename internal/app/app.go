package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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
	DatabaseURL    string
	MigrationsURL  string
	MigrateOnStart bool   // true — применить миграции при старте; false — только проверить версию схемы
	APIKey         string // пусто — API без авторизации (только с AUTH_DISABLED=true)
	RateLimitRPS   float64
	RateLimitBurst int
	RealIPHeader   string // откуда брать IP клиента для лимита; пусто — адрес соединения
	Location       *time.Location
	Logger         *slog.Logger
}

// requestTimeout — сколько максимум обрабатывается один запрос, включая запросы к БД.
const requestTimeout = 10 * time.Second

// publicPaths доступны без API-ключа: проверка здоровья и документация.
var publicPaths = []string{"/healthz", "/swagger", "/openapi.yaml"}

// ConfigFromEnv читает настройки из переменных окружения (список — в README).
func ConfigFromEnv(logger *slog.Logger) (Config, error) {
	cfg := Config{
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		MigrationsURL: os.Getenv("DATABASE_URL_UNPOOLED"),
		APIKey:        os.Getenv("API_KEY"),
		RealIPHeader:  os.Getenv("REAL_IP_HEADER"),
		Location:      time.Local,
		Logger:        logger,
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("не задана переменная окружения DATABASE_URL")
	}

	if tz := os.Getenv("APP_TZ"); tz != "" {
		var err error
		if cfg.Location, err = time.LoadLocation(tz); err != nil {
			return Config{}, fmt.Errorf("APP_TZ: %w", err)
		}
	}

	// Без ключа сервер не стартует: забытая или опечатанная переменная на проде
	// иначе молча открыла бы API всем. Работать без ключа можно только явно.
	authDisabled, err := envBool("AUTH_DISABLED")
	if err != nil {
		return Config{}, err
	}
	switch {
	case authDisabled:
		cfg.APIKey = ""
	case cfg.APIKey == "":
		return Config{}, errors.New("не задан API_KEY (для локальной разработки без ключа — AUTH_DISABLED=true)")
	}

	if cfg.MigrateOnStart, err = envBool("MIGRATE_ON_START"); err != nil {
		return Config{}, err
	}

	if cfg.RateLimitRPS, err = envFloat("RATE_LIMIT_RPS", 20); err != nil {
		return Config{}, err
	}
	burst, err := envFloat("RATE_LIMIT_BURST", 40)
	if err != nil {
		return Config{}, err
	}
	cfg.RateLimitBurst = int(burst)

	// Vercel кладёт IP клиента в X-Real-IP и сам перезаписывает этот заголовок,
	// так что подделать его клиент не может. VERCEL=1 Vercel задаёт сам.
	if cfg.RealIPHeader == "" && os.Getenv("VERCEL") == "1" {
		cfg.RealIPHeader = "X-Real-IP"
	}
	return cfg, nil
}

func envBool(name string) (bool, error) {
	v := os.Getenv(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: ожидается true или false, получено %q", name, v)
	}
	return b, nil
}

func envFloat(name string, def float64) (float64, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("%s: ожидается неотрицательное число, получено %q", name, v)
	}
	return f, nil
}

// migrationsURL — к какой БД применять миграции: напрямую, если DATABASE_URL идёт через пулер.
func (cfg Config) migrationsURL() string {
	if cfg.MigrationsURL != "" {
		return cfg.MigrationsURL
	}
	return cfg.DatabaseURL
}

// New готовит схему БД, подключается к ней и возвращает готовый обработчик.
// Вызывающий обязан закрыть пул, вызвав close.
func New(ctx context.Context, cfg Config) (h http.Handler, close func(), err error) {
	if cfg.MigrateOnStart {
		if err := migrations.Up(cfg.migrationsURL()); err != nil {
			return nil, nil, err
		}
		cfg.Logger.Info("миграции применены")
	} else if err := migrations.Check(cfg.migrationsURL()); err != nil {
		return nil, nil, err
	}

	switch {
	case cfg.APIKey == "":
		cfg.Logger.Warn("AUTH_DISABLED=true — API доступен без авторизации")
	case len(cfg.APIKey) < 32:
		cfg.Logger.Warn("API_KEY короче 32 символов — его проще подобрать; сгенерируйте: openssl rand -hex 32")
	}
	if cfg.RateLimitRPS > 0 && cfg.RealIPHeader == "" {
		cfg.Logger.Info("лимит запросов считается по адресу соединения: REAL_IP_HEADER не задан")
	}

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, nil, err
	}
	cfg.Logger.Info("БД подключена", "tz", cfg.Location.String())
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

	mws := []func(http.Handler) http.Handler{
		middleware.RequestID(cfg.Logger), // первым: остальные пишут в лог с его request_id
		middleware.Logging(),
		middleware.Recover(),
		middleware.SecurityHeaders(),
	}
	if cfg.RateLimitRPS > 0 {
		// До проверки ключа: перебирать ключ тоже можно не чаще лимита.
		mws = append(mws, middleware.RateLimit(cfg.RateLimitRPS, cfg.RateLimitBurst, middleware.ClientIP(cfg.RealIPHeader)))
	}
	mws = append(mws,
		middleware.APIKey(cfg.APIKey, publicPaths...),
		middleware.Timeout(requestTimeout),
	)
	return middleware.Chain(mux, mws...)
}

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
