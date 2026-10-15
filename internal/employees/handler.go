package employees

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

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
	WorkingHours(ctx context.Context, employeeID string) ([]WorkingDay, error)
	SetWorkingHours(ctx context.Context, employeeID string, days []WorkingDay) error
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
	mux.HandleFunc("GET /employees/{id}/schedule", h.GetSchedule)
	mux.HandleFunc("PUT /employees/{id}/schedule", h.PutSchedule)
}

func (h *Handler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	days, err := h.storage.WorkingHours(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get schedule", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, Schedule{EmployeeID: id, Days: days})
}

// PutSchedule заменяет весь недельный график мастера тем, что пришло в теле.
func (h *Handler) PutSchedule(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	var in struct {
		Days []WorkingDay `json:"days"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if checkDays(in.Days).Write(w) {
		return
	}
	err := h.storage.SetWorkingHours(r.Context(), id, in.Days)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "set schedule", err)
		return
	}
	if in.Days == nil {
		in.Days = []WorkingDay{}
	}
	httpx.WriteJSON(w, http.StatusOK, Schedule{EmployeeID: id, Days: in.Days})
}

// checkDays проверяет график: день недели 1–7 без повторов, время ЧЧ:ММ, конец позже начала.
// Время приводится к виду 09:00, как его потом вернёт БД.
func checkDays(days []WorkingDay) httpx.Problems {
	p := httpx.Problems{}
	seen := map[int]bool{}
	for i := range days {
		d := &days[i]
		field := fmt.Sprintf("days[%d]", i)
		switch {
		case d.Weekday < 1 || d.Weekday > 7:
			p.Add(field+".weekday", "от 1 (понедельник) до 7 (воскресенье)")
		case seen[d.Weekday]:
			p.Add(field+".weekday", "этот день уже есть в графике")
		}
		seen[d.Weekday] = true

		start, startErr := time.Parse("15:04", d.Start)
		end, endErr := time.Parse("15:04", d.End)
		if startErr != nil {
			p.Add(field+".start", "время в формате ЧЧ:ММ, например 10:00")
		}
		if endErr != nil {
			p.Add(field+".end", "время в формате ЧЧ:ММ, например 19:00")
		}
		if startErr == nil && endErr == nil {
			if !end.After(start) {
				p.Add(field+".end", "должно быть позже start")
			}
			d.Start, d.End = start.Format("15:04"), end.Format("15:04")
		}
	}
	return p
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
