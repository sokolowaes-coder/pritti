package records

import (
	"context"
	"errors"
	"net/http"
	"time"

	"pritti/internal/httpx"
)

const (
	maxNameLen        = 200
	maxDescriptionLen = 2000
)

type RecordStore interface {
	List(ctx context.Context, f ListFilter) ([]Record, int, error)
	GetByID(ctx context.Context, id string) (Record, error)
	Create(ctx context.Context, in RecordInput) (Record, error)
	Update(ctx context.Context, id string, in RecordInput) (Record, error)
	Delete(ctx context.Context, id string) error
}

var _ RecordStore = (*Storage)(nil)

type Handler struct {
	storage RecordStore
	loc     *time.Location // пояс для дат без пояса в фильтрах
}

func NewHandler(storage RecordStore, loc *time.Location) *Handler {
	return &Handler{storage: storage, loc: loc}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	f, err := ParseListFilter(r.URL.Query(), h.loc)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	records, total, err := h.storage.List(r.Context(), f)
	if err != nil {
		httpx.InternalError(w, "list records", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, newListResponse(f, records, total, h.loc))
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	rec, err := h.storage.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "запись не найдена")
		return
	}
	if err != nil {
		httpx.InternalError(w, "get record", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	rec, err := h.storage.Create(r.Context(), in)
	if errors.Is(err, ErrClientNotFound) || errors.Is(err, ErrEmployeeNotFound) {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		httpx.InternalError(w, "create record", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rec)
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
	rec, err := h.storage.Update(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "запись не найдена")
		return
	}
	if errors.Is(err, ErrClientNotFound) || errors.Is(err, ErrEmployeeNotFound) {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		httpx.InternalError(w, "update record", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	err := h.storage.Delete(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "запись не найдена")
		return
	}
	if err != nil {
		httpx.InternalError(w, "delete record", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /records", h.List)
	mux.HandleFunc("POST /records", h.Create)
	mux.HandleFunc("GET /records/{id}", h.GetByID)
	mux.HandleFunc("PUT /records/{id}", h.Update)
	mux.HandleFunc("DELETE /records/{id}", h.Delete)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (RecordInput, bool) {
	var in RecordInput
	if !httpx.DecodeJSON(w, r, &in) {
		return RecordInput{}, false
	}
	msg := httpx.CheckText("name", &in.Name, true, maxNameLen)
	if msg == "" {
		msg = httpx.CheckText("description", &in.Description, false, maxDescriptionLen)
	}
	switch {
	case msg != "":
	case in.ClientID == "":
		msg = "client_id обязателен"
	case !httpx.IsUUID(in.ClientID):
		msg = "client_id: неверный id"
	case in.EmployeeID == "":
		msg = "employee_id обязателен"
	case !httpx.IsUUID(in.EmployeeID):
		msg = "employee_id: неверный id"
	}
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, msg)
		return RecordInput{}, false
	}
	return in, true
}
