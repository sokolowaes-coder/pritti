package employees

import (
	"context"
	"errors"
	"net/http"

	"pritti/internal/httpx"
)

const maxNameLen = 200

type EmployeeStore interface {
	List(ctx context.Context) ([]Employee, error)
	Create(ctx context.Context, in EmployeeInput) (Employee, error)
	Update(ctx context.Context, id string, in EmployeeInput) (Employee, error)
	Delete(ctx context.Context, id string) error
}

var _ EmployeeStore = (*Storage)(nil)

type Handler struct {
	storage EmployeeStore
}

func NewHandler(storage EmployeeStore) *Handler {
	return &Handler{storage: storage}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	list, err := h.storage.List(r.Context())
	if err != nil {
		httpx.InternalError(w, "list employees", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "count": len(list), "data": list})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	e, err := h.storage.Create(r.Context(), in)
	if err != nil {
		httpx.InternalError(w, "create employee", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, e)
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
	e, err := h.storage.Update(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		httpx.InternalError(w, "update employee", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
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
		httpx.InternalError(w, "delete employee", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /employees", h.List)
	mux.HandleFunc("POST /employees", h.Create)
	mux.HandleFunc("PUT /employees/{id}", h.Update)
	mux.HandleFunc("DELETE /employees/{id}", h.Delete)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (EmployeeInput, bool) {
	var in EmployeeInput
	if !httpx.DecodeJSON(w, r, &in) {
		return EmployeeInput{}, false
	}
	if msg := httpx.CheckText("name", &in.Name, true, maxNameLen); msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return EmployeeInput{}, false
	}
	return in, true
}
