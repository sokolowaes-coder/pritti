// Package httpx — общие хелперы HTTP-хендлеров: JSON-ответы, ошибки, разбор id и тела.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
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

// DecodeJSON читает тело запроса (не больше 1 МБ) в v. При ошибке сам отвечает 400.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, "неверный JSON")
		return false
	}
	return true
}
