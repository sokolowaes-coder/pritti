package clients

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"pritti/internal/httpx"
)

const maxFullNameLen = 200

// Тексты ошибок для пользователя API.
const (
	msgNotFound   = "клиент не найден"
	msgPhoneTaken = "клиент с таким телефоном уже есть"
	msgHasRecords = "у клиента есть записи, сначала удалите или переназначьте их"
)

type ClientStore interface {
	List(ctx context.Context, limit, offset int) ([]Client, int, error)
	GetByID(ctx context.Context, id string) (Client, error)
	Create(ctx context.Context, in ClientInput) (Client, error)
	Update(ctx context.Context, id string, in ClientInput) (Client, error)
	Delete(ctx context.Context, id string) error
}

var _ ClientStore = (*Storage)(nil)

type Handler struct {
	storage ClientStore
}

func NewHandler(storage ClientStore) *Handler {
	return &Handler{storage: storage}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, total, err := h.storage.List(r.Context(), page.Limit, page.Offset)
	if err != nil {
		httpx.InternalError(w, "list clients", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.ListResponse[Client]{Success: true, Count: total, Pagination: page, Data: list})
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	c, err := h.storage.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, "get client", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	c, err := h.storage.Create(r.Context(), in)
	if errors.Is(err, ErrPhoneTaken) {
		httpx.WriteError(w, http.StatusConflict, msgPhoneTaken)
		return
	}
	if err != nil {
		httpx.InternalError(w, "create client", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	c, err := h.storage.Update(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, msgNotFound)
		return
	}
	if errors.Is(err, ErrPhoneTaken) {
		httpx.WriteError(w, http.StatusConflict, msgPhoneTaken)
		return
	}
	if err != nil {
		httpx.InternalError(w, "update client", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	err := h.storage.Delete(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, msgNotFound)
	case errors.Is(err, ErrHasRecords):
		httpx.WriteError(w, http.StatusConflict, msgHasRecords)
	case err != nil:
		httpx.InternalError(w, "delete client", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /clients", h.List)
	mux.HandleFunc("POST /clients", h.Create)
	mux.HandleFunc("GET /clients/{id}", h.GetByID)
	mux.HandleFunc("PUT /clients/{id}", h.Update)
	mux.HandleFunc("DELETE /clients/{id}", h.Delete)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (ClientInput, bool) {
	var in ClientInput
	if !httpx.DecodeJSON(w, r, &in) {
		return ClientInput{}, false
	}
	if msg := httpx.CheckText("full_name", &in.FullName, true, maxFullNameLen); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return ClientInput{}, false
	}
	if strings.TrimSpace(in.Phone) == "" {
		httpx.WriteError(w, http.StatusBadRequest, "phone обязателен")
		return ClientInput{}, false
	}
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return ClientInput{}, false
	}
	in.Phone = phone
	return in, true
}
