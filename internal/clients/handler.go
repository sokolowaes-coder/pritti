package clients

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"pritti/internal/httpx"
)

const maxFullNameLen = 200

// Коды и тексты ошибок для пользователя API.
const (
	codeNotFound   = "client_not_found"
	codePhoneTaken = "phone_taken"
	codeHasRecords = "client_has_records"

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
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, err.Error())
		return
	}
	list, total, err := h.storage.List(r.Context(), page.Limit, page.Offset)
	if err != nil {
		httpx.InternalError(w, r, "list clients", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get client", err)
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
		httpx.WriteError(w, http.StatusConflict, codePhoneTaken, msgPhoneTaken)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "create client", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if errors.Is(err, ErrPhoneTaken) {
		httpx.WriteError(w, http.StatusConflict, codePhoneTaken, msgPhoneTaken)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "update client", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
	case errors.Is(err, ErrHasRecords):
		httpx.WriteError(w, http.StatusConflict, codeHasRecords, msgHasRecords)
	case err != nil:
		httpx.InternalError(w, r, "delete client", err)
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
	p := httpx.Problems{}
	p.Text("full_name", &in.FullName, true, maxFullNameLen)
	if strings.TrimSpace(in.Phone) == "" {
		p.Add("phone", "обязательное поле")
	} else if phone, err := NormalizePhone(in.Phone); err != nil {
		p.Add("phone", err.Error())
	} else {
		in.Phone = phone
	}
	if p.Write(w) {
		return ClientInput{}, false
	}
	return in, true
}
