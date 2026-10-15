package services

import (
	"context"
	"errors"
	"net/http"

	"pritti/internal/httpx"
)

const (
	maxNameLen  = 200
	minDuration = 5
	maxDuration = 720         // 12 часов — как предел длины записи
	maxPriceKop = 100_000_000 // 1 000 000 ₽: больше — почти наверняка лишние нули
)

// Коды и тексты ошибок для пользователя API.
const (
	codeNotFound   = "service_not_found"
	codeNameTaken  = "service_name_taken"
	codeHasRecords = "service_has_records"

	msgNotFound   = "услуга не найдена"
	msgNameTaken  = "услуга с таким названием уже есть"
	msgHasRecords = "на услугу есть записи, сначала удалите или переназначьте их"
)

type ServiceStore interface {
	List(ctx context.Context, limit, offset int) ([]Service, int, error)
	GetByID(ctx context.Context, id string) (Service, error)
	Create(ctx context.Context, in ServiceInput) (Service, error)
	Update(ctx context.Context, id string, in ServiceInput) (Service, error)
	Delete(ctx context.Context, id string) error
}

var _ ServiceStore = (*Storage)(nil)

type Handler struct {
	storage ServiceStore
}

func NewHandler(storage ServiceStore) *Handler {
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
		httpx.InternalError(w, r, "list services", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.ListResponse[Service]{Success: true, Count: total, Pagination: page, Data: list})
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	sv, err := h.storage.GetByID(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get service", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, sv)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	sv, err := h.storage.Create(r.Context(), in)
	if errors.Is(err, ErrNameTaken) {
		httpx.WriteError(w, http.StatusConflict, codeNameTaken, msgNameTaken)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "create service", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, sv)
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
	sv, err := h.storage.Update(r.Context(), id, in)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
	case errors.Is(err, ErrNameTaken):
		httpx.WriteError(w, http.StatusConflict, codeNameTaken, msgNameTaken)
	case err != nil:
		httpx.InternalError(w, r, "update service", err)
	default:
		httpx.WriteJSON(w, http.StatusOK, sv)
	}
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
		httpx.InternalError(w, r, "delete service", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /services", h.List)
	mux.HandleFunc("POST /services", h.Create)
	mux.HandleFunc("GET /services/{id}", h.GetByID)
	mux.HandleFunc("PUT /services/{id}", h.Update)
	mux.HandleFunc("DELETE /services/{id}", h.Delete)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (ServiceInput, bool) {
	var in ServiceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return ServiceInput{}, false
	}
	p := httpx.Problems{}
	p.Text("name", &in.Name, true, maxNameLen)
	if in.DurationMinutes < minDuration || in.DurationMinutes > maxDuration {
		p.Add("duration_minutes", "от 5 до 720 минут")
	}
	if in.PriceKopecks < 0 || in.PriceKopecks > maxPriceKop {
		p.Add("price_kopecks", "от 0 до 100000000 (1 000 000 ₽)")
	}
	if p.Write(w) {
		return ServiceInput{}, false
	}
	return in, true
}
