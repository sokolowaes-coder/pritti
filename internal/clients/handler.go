package clients

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"pritti/internal/httpx"
)

const maxFullNameLen = 200

type ClientStore interface {
	List(ctx context.Context) ([]Client, error)
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
	list, err := h.storage.List(r.Context())
	if err != nil {
		httpx.InternalError(w, "list clients", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "count": len(list), "data": list})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	c, err := h.storage.Create(r.Context(), in)
	if errors.Is(err, ErrPhoneTaken) {
		httpx.WriteError(w, http.StatusConflict, err.Error())
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
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, ErrPhoneTaken) {
		httpx.WriteError(w, http.StatusConflict, err.Error())
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
		httpx.WriteError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrHasRecords):
		httpx.WriteError(w, http.StatusConflict, err.Error())
	case err != nil:
		httpx.InternalError(w, "delete client", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /clients", h.List)
	mux.HandleFunc("POST /clients", h.Create)
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
