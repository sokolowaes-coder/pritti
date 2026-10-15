package employees

import (
	"context"
	"errors"
	"net/http"

	"pritti/internal/httpx"
)

const maxNameLen = 200

// Коды и тексты ошибок для пользователя API.
const (
	codeNotFound   = "employee_not_found"
	codeHasRecords = "employee_has_records"

	msgNotFound   = "сотрудник не найден"
	msgHasRecords = "у сотрудника есть записи, сначала удалите или переназначьте их"
)

type EmployeeStore interface {
	List(ctx context.Context, limit, offset int) ([]Employee, int, error)
	GetByID(ctx context.Context, id string) (Employee, error)
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
	page, err := httpx.ParsePage(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, err.Error())
		return
	}
	list, total, err := h.storage.List(r.Context(), page.Limit, page.Offset)
	if err != nil {
		httpx.InternalError(w, r, "list employees", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.ListResponse[Employee]{Success: true, Count: total, Pagination: page, Data: list})
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	e, err := h.storage.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get employee", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	e, err := h.storage.Create(r.Context(), in)
	if err != nil {
		httpx.InternalError(w, r, "create employee", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "update employee", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
	case errors.Is(err, ErrHasRecords):
		httpx.WriteError(w, http.StatusConflict, codeHasRecords, msgHasRecords)
	case err != nil:
		httpx.InternalError(w, r, "delete employee", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /employees", h.List)
	mux.HandleFunc("POST /employees", h.Create)
	mux.HandleFunc("GET /employees/{id}", h.GetByID)
	mux.HandleFunc("PUT /employees/{id}", h.Update)
	mux.HandleFunc("DELETE /employees/{id}", h.Delete)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (EmployeeInput, bool) {
	var in EmployeeInput
	if !httpx.DecodeJSON(w, r, &in) {
		return EmployeeInput{}, false
	}
	p := httpx.Problems{}
	p.Text("name", &in.Name, true, maxNameLen)
	if p.Write(w) {
		return EmployeeInput{}, false
	}
	return in, true
}
