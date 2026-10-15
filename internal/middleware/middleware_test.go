package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pritti/internal/httpx"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestLoggingWritesRequest(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		handler   http.HandlerFunc
		wantLevel string
		wantInLog string
	}{
		{
			name:      "статус из WriteHeader",
			path:      "/records",
			handler:   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) },
			wantLevel: "level=INFO",
			wantInLog: "status=201",
		},
		{
			name:      "без WriteHeader — 200",
			path:      "/records",
			handler:   func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) },
			wantLevel: "level=INFO",
			wantInLog: "status=200",
		},
		{
			name:      "5xx пишется как ошибка",
			path:      "/records",
			handler:   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			wantLevel: "level=ERROR",
			wantInLog: "status=500",
		},
		{
			name:      "healthz — только DEBUG",
			path:      "/healthz",
			handler:   func(w http.ResponseWriter, r *http.Request) {},
			wantLevel: "level=DEBUG",
			wantInLog: "path=/healthz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := Chain(tt.handler, RequestID(newTestLogger(&buf)), Logging())
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", tt.path, nil))

			log := buf.String()
			for _, want := range []string{tt.wantLevel, tt.wantInLog, "method=POST", "duration=", "request_id="} {
				if !strings.Contains(log, want) {
					t.Errorf("в логе нет %q:\n%s", want, log)
				}
			}
		})
	}
}

func TestRecoverReturns500(t *testing.T) {
	var buf bytes.Buffer
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("что-то сломалось")
	}), RequestID(newTestLogger(&buf)), Recover())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/records", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("статус = %d, ожидали 500", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"success":false,"code":"internal_error","error":"внутренняя ошибка"}` {
		t.Errorf("тело = %s, ожидали JSON-ошибку", got)
	}
	if strings.Contains(rec.Body.String(), "что-то сломалось") {
		t.Errorf("текст паники утёк клиенту: %s", rec.Body)
	}
	if log := buf.String(); !strings.Contains(log, "что-то сломалось") || !strings.Contains(log, "stack=") {
		t.Errorf("в логе нет паники со стеком:\n%s", log)
	}
}

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}), mw("first"), mw("second"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if got := strings.Join(order, ","); got != "first,second,handler" {
		t.Errorf("порядок = %s", got)
	}
}

// Паника под Logging+Recover должна попасть в лог запроса как 500.
func TestLoggingSeesRecoveredPanic(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("бум")
	}), RequestID(logger), Logging(), Recover())

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/records", nil))

	if !strings.Contains(buf.String(), "msg=request") || !strings.Contains(buf.String(), "status=500") {
		t.Errorf("в логе нет запроса со статусом 500:\n%s", buf.String())
	}
}

func TestAPIKey(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	tests := []struct {
		name       string
		key        string
		path       string
		header     string
		wantStatus int
	}{
		{"ключ не настроен — всё открыто", "", "/records", "", 200},
		{"верный ключ", "secret", "/records", "secret", 200},
		{"без ключа", "secret", "/records", "", 401},
		{"неверный ключ", "secret", "/records", "secrex", 401},
		{"публичный путь без ключа", "secret", "/healthz", "", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.header != "" {
				req.Header.Set(APIKeyHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			APIKey(tt.key, "/healthz")(ok).ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("статус = %d, ожидали %d", rec.Code, tt.wantStatus)
			}
			if rec.Code == 401 && !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
				t.Errorf("тело 401 не в формате ошибки: %s", rec.Body)
			}
		})
	}
}

func TestTimeoutSetsDeadline(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	h := Timeout(time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, hasDeadline = r.Context().Deadline()
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !hasDeadline || time.Until(deadline) > time.Second {
		t.Errorf("deadline = %v, есть = %v", deadline, hasDeadline)
	}
}

func TestRequestID(t *testing.T) {
	var buf bytes.Buffer
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Logger(r.Context()).Info("из хендлера")
		seen = w.Header().Get(RequestIDHeader)
	}), RequestID(newTestLogger(&buf)))

	tests := []struct {
		name, incoming string
		keep           bool
	}{
		{"нет заголовка — свой id", "", false},
		{"id от прокси сохраняется", "nginx-abc_123", true},
		{"длинный id заменяется", strings.Repeat("a", 65), false},
		{"опасные символы — заменяется", "abc\nlevel=ERROR", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			req := httptest.NewRequest("GET", "/", nil)
			if tt.incoming != "" {
				req.Header.Set(RequestIDHeader, tt.incoming)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			id := rec.Header().Get(RequestIDHeader)
			if id == "" || id != seen {
				t.Fatalf("id в ответе %q, в хендлере %q", id, seen)
			}
			if tt.keep != (id == tt.incoming) {
				t.Errorf("id = %q, входящий %q, ожидали сохранить: %v", id, tt.incoming, tt.keep)
			}
			if !strings.Contains(buf.String(), "request_id="+id) {
				t.Errorf("лог хендлера без request_id:\n%s", buf.String())
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, ожидали %q", header, got, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Real-IP", "203.0.113.7")
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.1")

	tests := []struct{ header, want string }{
		{"", "10.0.0.1"}, // без прокси заголовкам не верим
		{"X-Real-IP", "203.0.113.7"},
		{"X-Forwarded-For", "198.51.100.1"},
		{"X-Missing", "10.0.0.1"}, // заголовка нет — адрес соединения
	}
	for _, tt := range tests {
		if got := ClientIP(tt.header)(req); got != tt.want {
			t.Errorf("ClientIP(%q) = %q, ожидали %q", tt.header, got, tt.want)
		}
	}
}

func TestRateLimit(t *testing.T) {
	h := RateLimit(1, 2, ClientIP("X-Real-IP"))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	call := func(ip string) int {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Real-IP", ip)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Всплеск 2 — проходят два запроса подряд, третий получает 429.
	for i, want := range []int{200, 200, 429} {
		if got := call("1.1.1.1"); got != want {
			t.Errorf("запрос %d: статус %d, ожидали %d", i+1, got, want)
		}
	}
	// У другого IP свой счётчик.
	if got := call("2.2.2.2"); got != 200 {
		t.Errorf("другой IP: статус %d, ожидали 200", got)
	}
}

func TestIPLimiterForgetsIdleVisitors(t *testing.T) {
	l := &ipLimiter{limit: 1, burst: 1, visitors: map[string]*visitor{}}
	start := time.Now()
	l.allow("1.1.1.1", start)
	l.allow("2.2.2.2", start.Add(visitorTTL))
	// Через visitorTTL+ после первого: 1.1.1.1 давно молчит и забывается, 2.2.2.2 — нет.
	l.allow("3.3.3.3", start.Add(visitorTTL+2*time.Minute))
	if _, ok := l.visitors["1.1.1.1"]; ok {
		t.Error("давно не приходивший IP не забыт")
	}
	if _, ok := l.visitors["2.2.2.2"]; !ok {
		t.Error("недавний IP забыт раньше времени")
	}
}
