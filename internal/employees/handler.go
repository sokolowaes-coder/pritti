package employees

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"pritti/internal/httpx"
	"pritti/internal/services"
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
	Services(ctx context.Context, employeeID string) ([]services.Service, error)
	SetServices(ctx context.Context, employeeID string, serviceIDs []string) error
	ByService(ctx context.Context, serviceID string) ([]Employee, error)
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
	mux.HandleFunc("GET /employees/{id}/services", h.GetServices)
	mux.HandleFunc("PUT /employees/{id}/services", h.PutServices)
	// Путь под /services, но отвечает про мастеров — поэтому здесь.
	mux.HandleFunc("GET /services/{id}/employees", h.ByService)
}

// EmployeeServices — услуги, которые оказывает мастер.
type EmployeeServices struct {
	EmployeeID string             `json:"employee_id"`
	Services   []services.Service `json:"services"`
}

func (h *Handler) GetServices(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	list, err := h.storage.Services(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "get employee services", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, EmployeeServices{EmployeeID: id, Services: list})
}

// PutServices заменяет весь набор услуг мастера: {"service_ids": ["<uuid>", ...]}.
// Отвечает тем же, что GetServices, — с названиями и длительностями.
func (h *Handler) PutServices(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	var in struct {
		ServiceIDs []string `json:"service_ids"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	p := httpx.Problems{}
	ids := make([]string, 0, len(in.ServiceIDs))
	seen := map[string]bool{}
	for i := range in.ServiceIDs {
		p.UUID(fmt.Sprintf("service_ids[%d]", i), &in.ServiceIDs[i])
		if id := in.ServiceIDs[i]; httpx.IsUUID(id) && !seen[id] { // повторы просто схлопываем
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if p.Write(w) {
		return
	}

	err := h.storage.SetServices(r.Context(), id, ids)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, codeNotFound, msgNotFound)
		return
	case errors.Is(err, ErrServiceNotFound):
		httpx.Problems{"service_ids": "есть несуществующая услуга"}.Write(w)
		return
	case err != nil:
		httpx.InternalError(w, r, "set employee services", err)
		return
	}
	h.GetServices(w, r)
}

// ByService — GET /services/{id}/employees: кто оказывает услугу.
func (h *Handler) ByService(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	list, err := h.storage.ByService(r.Context(), id)
	if errors.Is(err, ErrServiceNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "service_not_found", "услуга не найдена")
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "employees by service", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "service_id": id, "count": len(list), "data": list})
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
