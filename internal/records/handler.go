package records

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"pritti/internal/httpx"
)

const (
	maxNameLen        = 200
	maxDescriptionLen = 2000
	maxDuration       = 12 * time.Hour // длиннее — почти наверняка опечатка в дате
)

// Коды и тексты ошибок для пользователя API.
const (
	codeNotFound     = "record_not_found"
	codeEmployeeBusy = "employee_busy"

	msgNotFound     = "запись не найдена"
	msgEmployeeBusy = "у мастера уже есть запись, пересекающаяся с этим временем"
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
	loc     *time.Location // пояс для дат без пояса в фильтрах и в теле запроса
}

func NewHandler(storage RecordStore, loc *time.Location) *Handler {
	return &Handler{storage: storage, loc: loc}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	f, err := ParseListFilter(r.URL.Query(), h.loc)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, err.Error())
		return
	}
	records, total, err := h.storage.List(r.Context(), f)
	if err != nil {
		httpx.InternalError(w, r, "list records", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get record", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	in, ok := h.decodeInput(w, r)
	if !ok {
		return
	}
	rec, err := h.storage.Create(r.Context(), in)
	if writeStoreError(w, err) {
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "create record", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, rec)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	in, ok := h.decodeInput(w, r)
	if !ok {
		return
	}
	rec, err := h.storage.Update(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if writeStoreError(w, err) {
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "update record", err)
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
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "delete record", err)
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

// writeStoreError отвечает на ошибки, о которых знает только БД: клиента или сотрудника
// из тела запроса нет (400, как ошибка поля) или мастер в это время занят (409).
func writeStoreError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrClientNotFound):
		return httpx.Problems{"client_id": "клиент не найден"}.Write(w)
	case errors.Is(err, ErrEmployeeNotFound):
		return httpx.Problems{"employee_id": "сотрудник не найден"}.Write(w)
	case errors.Is(err, ErrEmployeeBusy):
		httpx.WriteError(w, http.StatusConflict, codeEmployeeBusy, msgEmployeeBusy)
		return true
	}
	return false
}

// recordRequest — тело POST и PUT. Время — строками, чтобы принять и время без пояса
// (как в фильтрах) и ответить понятной ошибкой поля, а не общим «неверный JSON».
type recordRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ClientID    string `json:"client_id"`
	EmployeeID  string `json:"employee_id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	Status      string `json:"status"` // пусто — scheduled
}

func (h *Handler) decodeInput(w http.ResponseWriter, r *http.Request) (RecordInput, bool) {
	var req recordRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return RecordInput{}, false
	}
	p := httpx.Problems{}
	p.Text("name", &req.Name, true, maxNameLen)
	p.Text("description", &req.Description, false, maxDescriptionLen)
	p.UUID("client_id", &req.ClientID)
	p.UUID("employee_id", &req.EmployeeID)
	startsAt := h.requiredTime(p, "starts_at", req.StartsAt)
	endsAt := h.requiredTime(p, "ends_at", req.EndsAt)
	if !startsAt.IsZero() && !endsAt.IsZero() {
		switch d := endsAt.Sub(startsAt); {
		case d <= 0:
			p.Add("ends_at", "должно быть позже starts_at")
		case d > maxDuration:
			p.Add("ends_at", "запись не длиннее 12 часов")
		}
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = StatusScheduled
	} else if !slices.Contains(statuses, status) {
		p.Add("status", "одно из: "+strings.Join(statuses, ", "))
	}

	if p.Write(w) {
		return RecordInput{}, false
	}
	return RecordInput{
		Name:        req.Name,
		Description: req.Description,
		ClientID:    req.ClientID,
		EmployeeID:  req.EmployeeID,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		Status:      status,
	}, true
}

// requiredTime разбирает обязательное поле-время; при ошибке записывает её в p и возвращает ноль.
func (h *Handler) requiredTime(p httpx.Problems, field, s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		p.Add(field, "обязательное поле")
		return time.Time{}
	}
	t, ok := parseTime(s, h.loc)
	if !ok {
		p.Add(field, "неверное время, формат 2026-09-27T10:00:00 или 2026-09-27T10:00:00+03:00")
	}
	return t
}
