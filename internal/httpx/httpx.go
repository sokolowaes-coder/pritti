// Package httpx — общие хелперы HTTP-хендлеров: JSON-ответы, ошибки, разбор id и тела.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxBodyBytes — предел тела запроса: больше — 413.
const MaxBodyBytes = 1 << 20

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("запись JSON-ответа", "error", err)
	}
}

// WriteError отвечает в едином формате ошибок: {"success": false, "error": "..."}.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]any{"success": false, "error": msg})
}

// InternalError пишет настоящую ошибку в лог, а клиенту отдаёт только «внутренняя ошибка».
func InternalError(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "error", err)
	WriteError(w, http.StatusInternalServerError, "внутренняя ошибка")
}

// ParseID читает {id} из пути — UUID вида 01a140dc-0c34-7abb-8d6c-36e2d1879e0d.
// При ошибке сам отвечает 400 и возвращает false.
func ParseID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !IsUUID(id) {
		WriteError(w, http.StatusBadRequest, "неверный id")
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
		WriteError(w, http.StatusRequestEntityTooLarge, "тело запроса больше 1 МБ")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		WriteError(w, http.StatusBadRequest, "неизвестное поле "+strings.TrimPrefix(err.Error(), "json: unknown field "))
	case errors.Is(err, errTrailingData):
		WriteError(w, http.StatusBadRequest, "лишние данные после JSON")
	default:
		WriteError(w, http.StatusBadRequest, "неверный JSON")
	}
	return false
}

var errTrailingData = errors.New("лишние данные после JSON")

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

// CheckText обрезает пробелы по краям *s и проверяет длину.
// Возвращает текст ошибки для клиента или "", если всё в порядке.
func CheckText(field string, s *string, required bool, maxLen int) string {
	*s = strings.TrimSpace(*s)
	switch {
	case required && *s == "":
		return field + " обязателен"
	case utf8.RuneCountInString(*s) > maxLen:
		return field + ": не длиннее " + strconv.Itoa(maxLen) + " символов"
	}
	return ""
}
