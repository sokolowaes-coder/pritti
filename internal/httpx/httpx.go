// Package httpx — общие хелперы HTTP-хендлеров: JSON-ответы, ошибки, разбор id и тела.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
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

// ParseID читает {id} из пути. При ошибке сам отвечает 400 и возвращает false.
func ParseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "неверный id")
		return 0, false
	}
	return id, true
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
