// Package middleware — обёртки над http.Handler, общие для всех роутов.
package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"pritti/internal/httpx"
)

// Chain оборачивает h в middlewares; первый в списке выполняется первым.
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// RequestIDHeader — заголовок с id запроса: приходит от клиента или прокси, возвращается в ответе.
const RequestIDHeader = "X-Request-ID"

// RequestID даёт каждому запросу id и кладёт в контекст логгер с полем request_id.
// Все строки лога одного запроса (сам запрос, ошибки, паника) получают один id,
// а клиент видит его в заголовке ответа — по нему легко найти запрос в логах.
// Должен стоять в цепочке первым: остальные берут логгер из контекста.
func RequestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := httpx.WithLogger(r.Context(), base.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validRequestID принимает чужой id (например, от nginx), только если он короткий и из
// безопасных символов: иначе через заголовок в лог можно протащить что угодно.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.ContainsRune("-_.:", c)) {
			return false
		}
	}
	return true
}

func newRequestID() string {
	b := make([]byte, 8)
	rand.Read(b) // crypto/rand.Read не возвращает ошибок: при сбое программа падает сама
	return hex.EncodeToString(b)
}

// Logging пишет в лог каждый запрос: метод, путь, статус и время ответа.
// Запросы к /healthz пишутся на уровне DEBUG, чтобы healthcheck не засорял лог.
func Logging() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case r.URL.Path == "/healthz" && rec.status < 500:
				level = slog.LevelDebug
			case rec.status >= 500:
				level = slog.LevelError
			}
			httpx.Logger(r.Context()).Log(r.Context(), level, "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start).Round(time.Microsecond),
			)
		})
	}
}

// Recover перехватывает панику в хендлере: клиент получает 500,
// а в лог пишется ошибка со стеком. Без него паника обрывает соединение.
func Recover() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					if err == http.ErrAbortHandler {
						panic(err)
					}
					httpx.Logger(r.Context()).Error("panic",
						"method", r.Method,
						"path", r.URL.Path,
						"error", err,
						"stack", string(debug.Stack()),
					)
					httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "внутренняя ошибка")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders добавляет защитные заголовки ко всем ответам: браузер не угадывает тип
// содержимого, страницы нельзя встроить во фрейм (кликджекинг), Referer никуда не уходит.
// Здесь, а не в nginx: на Vercel nginx нет.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP возвращает функцию, определяющую IP клиента. header — заголовок, в который
// реальный IP кладёт прокси перед сервером (nginx и Vercel — X-Real-IP). Пустой header —
// брать адрес соединения: это верно, только если перед сервером прокси нет.
// Без прокси заголовку верить нельзя: клиент впишет туда что угодно и обойдёт лимит.
func ClientIP(header string) func(*http.Request) string {
	return func(r *http.Request) string {
		if header != "" {
			if v := r.Header.Get(header); v != "" {
				ip, _, _ := strings.Cut(v, ",") // в X-Forwarded-For первым идёт клиент
				return strings.TrimSpace(ip)
			}
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}

// RateLimit пропускает с одного IP в среднем rps запросов в секунду, всплеском — до burst.
// Лишние получают 429. Счётчики живут в памяти процесса: если экземпляров сервера несколько
// (Vercel), у каждого свои — это защита от перебора ключа и флуда, а не точная квота.
func RateLimit(rps float64, burst int, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	l := &ipLimiter{limit: rate.Limit(rps), burst: burst, visitors: map[string]*visitor{}}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r), time.Now()) {
				w.Header().Set("Retry-After", "1")
				httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeRateLimited, "слишком много запросов, повторите позже")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// visitorTTL — через сколько без запросов IP забывается, чтобы карта не росла бесконечно.
const visitorTTL = 3 * time.Minute

type ipLimiter struct {
	mu        sync.Mutex
	limit     rate.Limit
	burst     int
	visitors  map[string]*visitor
	lastSweep time.Time
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) > time.Minute {
		for ip, v := range l.visitors {
			if now.Sub(v.lastSeen) > visitorTTL {
				delete(l.visitors, ip)
			}
		}
		l.lastSweep = now
	}

	v, ok := l.visitors[ip]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.visitors[ip] = v
	}
	v.lastSeen = now
	return v.limiter.AllowN(now, 1)
}

// Timeout ограничивает время обработки запроса: по истечении d контекст запроса
// отменяется, и запросы к БД, которые его используют, прерываются.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// APIKeyHeader — заголовок, в котором клиент передаёт ключ.
const APIKeyHeader = "X-API-Key"

// APIKey пропускает только запросы с заголовком X-API-Key, равным key.
// Пути из public доступны без ключа. Пустой key выключает проверку — разрешает это
// только явная настройка AUTH_DISABLED (см. app.ConfigFromEnv).
func APIKey(key string, public ...string) func(http.Handler) http.Handler {
	open := make(map[string]bool, len(public))
	for _, p := range public {
		open[p] = true
	}
	return func(next http.Handler) http.Handler {
		if key == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get(APIKeyHeader)
			// ConstantTimeCompare: время сравнения не подсказывает, сколько символов угадано.
			if !open[r.URL.Path] && subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
				httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "нужен верный заголовок "+APIKeyHeader)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder запоминает статус ответа, чтобы Logging мог его записать.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// Unwrap нужен http.ResponseController, чтобы добраться до исходного writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
