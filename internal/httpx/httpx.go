// Package httpx — общие хелперы HTTP-хендлеров: JSON-ответы, ошибки, разбор id, тела и страниц.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxBodyBytes — предел тела запроса: больше — 413.
const MaxBodyBytes = 1 << 20

// Общие коды ошибок. Код — для программ (не меняется), текст — для людей (может меняться).
// Коды, относящиеся к одной сущности, объявлены в её пакете.
const (
	CodeInvalidID    = "invalid_id"
	CodeInvalidJSON  = "invalid_json"
	CodeBodyTooLarge = "body_too_large"
	CodeInvalidQuery = "invalid_query"
	CodeValidation   = "validation_error"
	CodeUnauthorized = "unauthorized"
	CodeRateLimited  = "rate_limited"
	CodeInternal     = "internal_error"
)

type loggerKey struct{}

// WithLogger кладёт в контекст логгер запроса (с его request_id).
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// Logger достаёт логгер запроса из контекста. Если его там нет (тесты, фоновые задачи) —
// общий slog.Default().
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Ошибка тут — почти всегда отключившийся клиент: статус уже отправлен, исправить нечего,
	// а сам запрос со статусом и так попадёт в лог через middleware.Logging.
	_ = json.NewEncoder(w).Encode(v)
}

// errorBody — единый формат ошибок: {"success": false, "code": "...", "error": "..."}.
type errorBody struct {
	Success bool              `json:"success"`
	Code    string            `json:"code"`
	Error   string            `json:"error"`
	Fields  map[string]string `json:"fields,omitempty"` // только у validation_error
}

func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, errorBody{Code: code, Error: msg})
}

// InternalError пишет настоящую ошибку в лог запроса, а клиенту отдаёт только «внутренняя ошибка».
func InternalError(w http.ResponseWriter, r *http.Request, op string, err error) {
	Logger(r.Context()).Error(op, "error", err)
	WriteError(w, http.StatusInternalServerError, CodeInternal, "внутренняя ошибка")
}

// ParseID читает {id} из пути — UUID вида 01a140dc-0c34-7abb-8d6c-36e2d1879e0d.
// При ошибке сам отвечает 400 и возвращает false.
func ParseID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !IsUUID(id) {
		WriteError(w, http.StatusBadRequest, CodeInvalidID, "неверный id")
		return "", false
	}
	return strings.ToLower(id), true
}

// IsUUID проверяет запись UUID: 8-4-4-4-12 шестнадцатеричных цифр.
// Проверка здесь, а не только в БД: иначе Postgres ответит ошибкой приведения типа — 500.
func IsUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// DecodeJSON строго читает тело запроса в v: ровно один JSON-объект, без неизвестных
// полей (опечатка в имени поля — ошибка, а не молча пустое значение), не больше MaxBodyBytes.
// При ошибке сам отвечает 400 (или 413) и возвращает false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errTrailingData
	}
	if err == nil {
		return true
	}

	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "тело запроса больше 1 МБ")
	// У encoding/json нет отдельного типа для неизвестного поля — только текст ошибки.
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "неизвестное поле "+strings.TrimPrefix(err.Error(), "json: unknown field "))
	case errors.Is(err, errTrailingData):
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "лишние данные после JSON")
	default:
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "неверный JSON")
	}
	return false
}

var errTrailingData = errors.New("лишние данные после JSON")

// Problems — ошибки проверки полей запроса: поле → что с ним не так.
// Собираются все сразу, чтобы клиент исправил форму за один раз, а не по полю за запрос.
type Problems map[string]string

// Add запоминает ошибку поля. Пустое msg — ошибки нет; у поля остаётся первая ошибка.
func (p Problems) Add(field, msg string) {
	if _, ok := p[field]; msg != "" && !ok {
		p[field] = msg
	}
}

// Text обрезает пробелы по краям *s и проверяет обязательность и длину (в символах, не байтах).
func (p Problems) Text(field string, s *string, required bool, maxLen int) {
	*s = strings.TrimSpace(*s)
	switch {
	case required && *s == "":
		p.Add(field, "обязательное поле")
	case utf8.RuneCountInString(*s) > maxLen:
		p.Add(field, "не длиннее "+strconv.Itoa(maxLen)+" символов")
	}
}

// UUID проверяет, что обязательное поле — UUID, и приводит его к нижнему регистру.
func (p Problems) UUID(field string, s *string) {
	*s = strings.ToLower(strings.TrimSpace(*s))
	switch {
	case *s == "":
		p.Add(field, "обязательное поле")
	case !IsUUID(*s):
		p.Add(field, "неверный id")
	}
}

// Write отвечает 400 со всеми ошибками и возвращает true, если они есть.
// В "error" — все ошибки одной строкой, в "fields" — по полям, для подсветки в форме.
func (p Problems) Write(w http.ResponseWriter) bool {
	if len(p) == 0 {
		return false
	}
	var parts []string
	for _, field := range slices.Sorted(maps.Keys(p)) {
		parts = append(parts, field+": "+p[field])
	}
	WriteJSON(w, http.StatusBadRequest, errorBody{Code: CodeValidation, Error: strings.Join(parts, "; "), Fields: p})
	return true
}

// Размер страницы в списках: ?limit=…&offset=….
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Page — какую страницу списка вернуть. В ответе — поле "pagination".
type Page struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ParsePage читает limit и offset из query. Без них — первые DefaultLimit элементов.
func ParsePage(q url.Values) (Page, error) {
	p := Page{Limit: DefaultLimit}
	var err error
	if v := q.Get("limit"); v != "" {
		if p.Limit, err = strconv.Atoi(v); err != nil || p.Limit < 1 || p.Limit > MaxLimit {
			return p, fmt.Errorf("limit: число от 1 до %d", MaxLimit)
		}
	}
	if v := q.Get("offset"); v != "" {
		if p.Offset, err = strconv.Atoi(v); err != nil || p.Offset < 0 {
			return p, errors.New("offset: число от 0")
		}
	}
	return p, nil
}

// ListResponse — ответ списка: count — сколько элементов всего, data — текущая страница.
type ListResponse[T any] struct {
	Success    bool `json:"success"`
	Count      int  `json:"count"`
	Pagination Page `json:"pagination"`
	Data       []T  `json:"data"`
}
