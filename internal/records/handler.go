package records

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"pritti/internal/httpx"
)

const (
	maxNameLen        = 200
	maxDescriptionLen = 2000
)

// Коды и тексты ошибок для пользователя API.
const (
	codeNotFound     = "record_not_found"
	codeEmployeeBusy = "employee_busy"
	codeClientBusy   = "client_busy"
	codeEmployeeGone = "employee_not_found"

	msgNotFound     = "запись не найдена"
	msgEmployeeBusy = "у мастера уже есть запись, пересекающаяся с этим временем"
	msgClientBusy   = "у клиента уже есть запись, пересекающаяся с этим временем"
	msgEmployeeGone = "сотрудник не найден"
)

type RecordStore interface {
	List(ctx context.Context, f ListFilter) ([]Record, int, error)
	GetByID(ctx context.Context, id string) (Record, error)
	Create(ctx context.Context, in RecordInput) (Record, error)
	Update(ctx context.Context, id string, in RecordInput) (Record, error)
	Delete(ctx context.Context, id string) error
	Busy(ctx context.Context, employeeID string, from, to time.Time) ([]Interval, error)
}

var _ RecordStore = (*Storage)(nil)

// Handler — HTTP-слой записей: разбирает и проверяет формат запроса, зовёт Booking
// и переводит его ошибки в статусы и коды ответа. Бизнес-правил здесь нет.
type Handler struct {
	booking *Booking
}

func NewHandler(booking *Booking) *Handler {
	return &Handler{booking: booking}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	f, err := ParseListFilter(r.URL.Query(), h.booking.loc)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, err.Error())
		return
	}
	records, total, err := h.booking.List(r.Context(), f)
	if err != nil {
		httpx.InternalError(w, r, "list records", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, newListResponse(f, records, total, h.booking.loc))
}

func (h *Handler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.ParseID(w, r)
	if !ok {
		return
	}
	rec, err := h.booking.Get(r.Context(), id)
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
	rec, err := h.booking.Create(r.Context(), in)
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
	rec, err := h.booking.Update(r.Context(), id, in)
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
	err := h.booking.Delete(r.Context(), id)
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
	mux.HandleFunc("GET /slots", h.Slots)
}

// writeStoreError отвечает на ошибки правил записи (Booking) и на те, о которых знает только БД:
// клиента, сотрудника или услуги нет (400, как ошибка поля), мастер или клиент заняты (409).
func writeStoreError(w http.ResponseWriter, err error) bool {
	var invalid ValidationError
	switch {
	case errors.As(err, &invalid):
		return httpx.Problems(invalid).Write(w)
	case errors.Is(err, ErrServiceNotFound):
		return httpx.Problems{"service_id": "услуга не найдена"}.Write(w)
	case errors.Is(err, ErrClientNotFound):
		return httpx.Problems{"client_id": "клиент не найден"}.Write(w)
	case errors.Is(err, ErrEmployeeNotFound):
		return httpx.Problems{"employee_id": "сотрудник не найден"}.Write(w)
	case errors.Is(err, ErrEmployeeBusy):
		httpx.WriteError(w, http.StatusConflict, codeEmployeeBusy, msgEmployeeBusy)
		return true
	case errors.Is(err, ErrClientBusy):
		httpx.WriteError(w, http.StatusConflict, codeClientBusy, msgClientBusy)
		return true
	}
	return false
}

// recordRequest — тело POST и PUT. Время — строками, чтобы принять и время без пояса
// (как в фильтрах) и ответить понятной ошибкой поля, а не общим «неверный JSON».
type recordRequest struct {
	Name        string `json:"name"` // пусто — название услуги
	Description string `json:"description"`
	ClientID    string `json:"client_id"`
	EmployeeID  string `json:"employee_id"`
	ServiceID   string `json:"service_id"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"` // пусто — по длительности услуги
	Status      string `json:"status"`  // пусто — scheduled
}

// decodeInput проверяет только формат полей. Обязательность названия и конца,
// длительность и статус проверяет Booking — там правила записи.
func (h *Handler) decodeInput(w http.ResponseWriter, r *http.Request) (Draft, bool) {
	var req recordRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return Draft{}, false
	}
	p := httpx.Problems{}
	p.Text("name", &req.Name, false, maxNameLen)
	p.Text("description", &req.Description, false, maxDescriptionLen)
	p.UUID("client_id", &req.ClientID)
	p.UUID("employee_id", &req.EmployeeID)
	if req.ServiceID = strings.TrimSpace(req.ServiceID); req.ServiceID != "" {
		p.UUID("service_id", &req.ServiceID)
	}
	startsAt := h.parseTime(p, "starts_at", req.StartsAt, true)
	endsAt := h.parseTime(p, "ends_at", req.EndsAt, false)
	// Без услуги название и конец обязательны. Главная проверка — в Booking, а здесь — заранее,
	// чтобы вместе с ошибками формата клиент сразу получил и эти, а не вторым запросом.
	if req.ServiceID == "" {
		if req.Name == "" {
			p.Add("name", "обязательное поле (или укажите service_id)")
		}
		if strings.TrimSpace(req.EndsAt) == "" {
			p.Add("ends_at", "обязательное поле (или укажите service_id — возьмём длительность услуги)")
		}
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = StatusScheduled
	} else if !slices.Contains(statuses, status) {
		p.Add("status", "одно из: "+strings.Join(statuses, ", "))
	}

	if p.Write(w) {
		return Draft{}, false
	}
	return Draft{
		Name:        req.Name,
		Description: req.Description,
		ClientID:    req.ClientID,
		EmployeeID:  req.EmployeeID,
		ServiceID:   req.ServiceID,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		Status:      status,
	}, true
}

// parseTime разбирает поле-время; при ошибке записывает её в p и возвращает ноль.
func (h *Handler) parseTime(p httpx.Problems, field, s string, required bool) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		if required {
			p.Add(field, "обязательное поле")
		}
		return time.Time{}
	}
	t, ok := parseTime(s, h.booking.loc)
	if !ok {
		p.Add(field, "неверное время, формат 2026-09-27T10:00:00 или 2026-09-27T10:00:00+03:00")
	}
	return t
}

// SlotsResponse — свободное время мастера на день.
type SlotsResponse struct {
	Success         bool       `json:"success"`
	EmployeeID      string     `json:"employee_id"`
	Date            string     `json:"date"`
	DurationMinutes int        `json:"duration_minutes"`
	Data            []Interval `json:"data"`
}

// Slots — GET /slots?employeeId=…&date=2026-10-20&serviceId=… (или &duration=60, в минутах):
// когда мастер свободен в этот день для визита такой длины.
func (h *Handler) Slots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	employeeID := strings.ToLower(strings.TrimSpace(q.Get("employeeId")))
	serviceID := strings.ToLower(strings.TrimSpace(q.Get("serviceId")))
	durationParam := strings.TrimSpace(q.Get("duration"))

	var problem string
	date, dateErr := time.ParseInLocation(time.DateOnly, q.Get("date"), h.booking.loc)
	minutes, minutesErr := strconv.Atoi(durationParam)
	switch {
	case !httpx.IsUUID(employeeID):
		problem = "employeeId: обязательный UUID мастера"
	case dateErr != nil:
		problem = "date: обязательная дата в формате 2026-10-20"
	case (serviceID == "") == (durationParam == ""):
		problem = "укажите что-то одно: serviceId (длительность услуги) или duration (минуты)"
	case serviceID != "" && !httpx.IsUUID(serviceID):
		problem = "serviceId: неверный id"
	case durationParam != "" && (minutesErr != nil || minutes < 5 || minutes > int(maxDuration/time.Minute)):
		problem = "duration: число минут от 5 до 720"
	}
	if problem != "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, problem)
		return
	}

	duration := time.Duration(minutes) * time.Minute
	var slots []Interval
	var err error
	if serviceID != "" {
		slots, duration, err = h.booking.SlotsForService(r.Context(), employeeID, serviceID, date)
	} else {
		slots, err = h.booking.Slots(r.Context(), employeeID, date, duration)
	}
	switch {
	case errors.Is(err, ErrEmployeeNotFound):
		httpx.WriteError(w, http.StatusNotFound, codeEmployeeGone, msgEmployeeGone)
		return
	case errors.Is(err, ErrServiceNotFound):
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, "serviceId: услуга не найдена")
		return
	case errors.Is(err, ErrServiceNotProvided):
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidQuery, "serviceId: мастер не оказывает эту услугу")
		return
	}
	if err != nil {
		httpx.InternalError(w, r, "slots", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, SlotsResponse{
		Success:         true,
		EmployeeID:      employeeID,
		Date:            date.Format(time.DateOnly),
		DurationMinutes: int(duration / time.Minute),
		Data:            slots,
	})
}
