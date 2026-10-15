package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pritti/internal/httpx"
)

// uid — n-й тестовый id: 00000000-0000-0000-0000-000000000001 и т. д.
func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

type fakeStore struct {
	records    []Record
	nextID     int
	refs       map[string]bool // id существующих клиентов и сотрудников
	err        error
	lastFilter ListFilter // с какими фильтрами вызвали List
}

// newFakeStore создаёт клиента и сотрудника uid(1) и записи с этими именами —
// по часу подряд с 16.10.2026 10:00 UTC, чтобы не пересекались.
func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{nextID: 1, refs: map[string]bool{uid(1): true}}
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for i, n := range names {
		at := start.Add(time.Duration(i) * time.Hour)
		s.Create(context.Background(), RecordInput{Name: n, ClientID: uid(1), EmployeeID: uid(1),
			StartsAt: at, EndsAt: at.Add(time.Hour), Status: StatusScheduled})
	}
	return s
}

// check повторяет проверки, которые в настоящей БД делает Postgres:
// внешние ключи и запрет пересечения записей одного мастера.
func (s *fakeStore) check(id string, in RecordInput) error {
	if !s.refs[in.ClientID] {
		return ErrClientNotFound
	}
	if !s.refs[in.EmployeeID] {
		return ErrEmployeeNotFound
	}
	if in.Status == StatusCancelled {
		return nil
	}
	for _, r := range s.records {
		if r.ID != id && r.EmployeeID == in.EmployeeID && r.Status != StatusCancelled &&
			r.StartsAt.Before(in.EndsAt) && in.StartsAt.Before(r.EndsAt) {
			return ErrEmployeeBusy
		}
	}
	return nil
}

// List в фейке только применяет пагинацию: сами фильтры проверяются
// в тестах хранилища на настоящей БД.
func (s *fakeStore) List(ctx context.Context, f ListFilter) ([]Record, int, error) {
	s.lastFilter = f
	if s.err != nil {
		return nil, 0, s.err
	}
	start := min(f.Offset, len(s.records))
	end := min(start+f.Limit, len(s.records))
	out := make([]Record, end-start)
	copy(out, s.records[start:end])
	return out, len(s.records), nil
}

func (s *fakeStore) GetByID(ctx context.Context, id string) (Record, error) {
	if s.err != nil {
		return Record{}, s.err
	}
	for _, r := range s.records {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, ErrNotFound
}

func (s *fakeStore) Create(ctx context.Context, in RecordInput) (Record, error) {
	if s.err != nil {
		return Record{}, s.err
	}
	if err := s.check("", in); err != nil {
		return Record{}, err
	}
	rec := Record{ID: uid(s.nextID), CreatedAt: time.Now()}
	apply(&rec, in)
	s.nextID++
	s.records = append(s.records, rec)
	return rec, nil
}

func (s *fakeStore) Update(ctx context.Context, id string, in RecordInput) (Record, error) {
	if s.err != nil {
		return Record{}, s.err
	}
	for i := range s.records {
		if s.records[i].ID == id {
			if err := s.check(id, in); err != nil {
				return Record{}, err
			}
			apply(&s.records[i], in)
			return s.records[i], nil
		}
	}
	return Record{}, ErrNotFound
}

func apply(r *Record, in RecordInput) {
	r.Name, r.Description = in.Name, in.Description
	r.ClientID, r.EmployeeID = in.ClientID, in.EmployeeID
	r.StartsAt, r.EndsAt, r.Status = in.StartsAt, in.EndsAt, in.Status
}

func (s *fakeStore) Delete(ctx context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	for i, r := range s.records {
		if r.ID == id {
			s.records = append(s.records[:i], s.records[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// do отправляет запрос в хендлер через настоящий роутер,
// чтобы заодно проверить пути и {id}.
func do(t *testing.T, store RecordStore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(store, time.UTC).Register(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// body — JSON тела записи: верные поля по умолчанию (визит 17.10 12:00–13:00, свободное
// время в newFakeStore), поверх — изменения. Значение nil убирает поле.
func body(changes map[string]any) string {
	b := map[string]any{
		"name":        "новая",
		"client_id":   uid(1),
		"employee_id": uid(1),
		"starts_at":   "2026-10-17T12:00:00",
		"ends_at":     "2026-10-17T13:00:00",
	}
	for k, v := range changes {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	out, _ := json.Marshal(b)
	return string(out)
}

type m = map[string]any

func TestHandlerStatusCodes(t *testing.T) {
	const one = "/records/00000000-0000-0000-0000-000000000001"
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"список", "GET", "/records", "", http.StatusOK},
		{"одна запись", "GET", one, "", http.StatusOK},
		{"нет записи", "GET", "/records/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"id не UUID", "GET", "/records/abc", "", http.StatusBadRequest},
		{"id — число", "GET", "/records/-1", "", http.StatusBadRequest},

		{"создание", "POST", "/records", body(nil), http.StatusCreated},
		{"создание с описанием и статусом", "POST", "/records", body(m{"description": "текст", "status": "completed"}), http.StatusCreated},
		{"создание: время с поясом", "POST", "/records", body(m{"starts_at": "2026-10-17T15:00:00+03:00", "ends_at": "2026-10-17T16:00:00+03:00"}), http.StatusCreated},
		{"создание: пустое имя", "POST", "/records", body(m{"name": ""}), http.StatusBadRequest},
		{"создание: имя из пробелов", "POST", "/records", body(m{"name": "   "}), http.StatusBadRequest},
		{"создание: без name", "POST", "/records", body(m{"name": nil}), http.StatusBadRequest},
		{"создание: без client_id", "POST", "/records", body(m{"client_id": nil}), http.StatusBadRequest},
		{"создание: без employee_id", "POST", "/records", body(m{"employee_id": nil}), http.StatusBadRequest},
		{"создание: client_id не UUID", "POST", "/records", body(m{"client_id": "abc"}), http.StatusBadRequest},
		{"создание: нет клиента", "POST", "/records", body(m{"client_id": uid(999)}), http.StatusBadRequest},
		{"создание: нет сотрудника", "POST", "/records", body(m{"employee_id": uid(999)}), http.StatusBadRequest},
		{"создание: без starts_at", "POST", "/records", body(m{"starts_at": nil}), http.StatusBadRequest},
		{"создание: без ends_at", "POST", "/records", body(m{"ends_at": nil}), http.StatusBadRequest},
		{"создание: кривое время", "POST", "/records", body(m{"starts_at": "17.10.2026 12:00"}), http.StatusBadRequest},
		{"создание: только дата", "POST", "/records", body(m{"starts_at": "2026-10-17"}), http.StatusBadRequest},
		{"создание: конец раньше начала", "POST", "/records", body(m{"ends_at": "2026-10-17T11:00:00"}), http.StatusBadRequest},
		{"создание: конец равен началу", "POST", "/records", body(m{"ends_at": "2026-10-17T12:00:00"}), http.StatusBadRequest},
		{"создание: длиннее 12 часов", "POST", "/records", body(m{"ends_at": "2026-10-18T12:00:00"}), http.StatusBadRequest},
		{"создание: неизвестный статус", "POST", "/records", body(m{"status": "done"}), http.StatusBadRequest},
		{"создание: мастер занят", "POST", "/records", body(m{"starts_at": "2026-10-16T10:30:00", "ends_at": "2026-10-16T11:30:00"}), http.StatusConflict},
		{"создание: впритык после чужой записи", "POST", "/records", body(m{"starts_at": "2026-10-16T11:00:00", "ends_at": "2026-10-16T12:00:00"}), http.StatusCreated},
		{"создание: отменённая не мешает пересечению", "POST", "/records", body(m{"starts_at": "2026-10-16T10:30:00", "ends_at": "2026-10-16T11:30:00", "status": "cancelled"}), http.StatusCreated},
		{"создание: сломанный JSON", "POST", "/records", `{`, http.StatusBadRequest},
		{"создание: имя длиннее 200", "POST", "/records", body(m{"name": strings.Repeat("я", 201)}), http.StatusBadRequest},
		{"создание: опечатка в поле", "POST", "/records", body(m{"clientid": uid(1)}), http.StatusBadRequest},

		{"изменение", "PUT", one, body(nil), http.StatusOK},
		{"изменение: своё же время не конфликтует", "PUT", one, body(m{"starts_at": "2026-10-16T10:00:00", "ends_at": "2026-10-16T10:30:00"}), http.StatusOK},
		{"изменение: нет записи", "PUT", "/records/00000000-0000-0000-0000-000000000999", body(nil), http.StatusNotFound},
		{"изменение: пустое имя", "PUT", one, body(m{"name": ""}), http.StatusBadRequest},
		{"изменение: без client_id", "PUT", one, body(m{"client_id": nil}), http.StatusBadRequest},
		{"изменение: нет клиента", "PUT", one, body(m{"client_id": uid(999)}), http.StatusBadRequest},
		{"изменение: id не UUID", "PUT", "/records/abc", body(nil), http.StatusBadRequest},

		{"удаление", "DELETE", one, "", http.StatusNoContent},
		{"удаление: нет записи", "DELETE", "/records/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"удаление: id не UUID", "DELETE", "/records/abc", "", http.StatusBadRequest},

		{"неподдерживаемый метод", "PATCH", one, "", http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore("первая")
			rec := do(t, store, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus {
				t.Errorf("статус = %d, ожидали %d; тело: %s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

func TestHandlerStoreError(t *testing.T) {
	requests := []struct {
		method, path, body string
	}{
		{"GET", "/records", ""},
		{"GET", "/records/00000000-0000-0000-0000-000000000001", ""},
		{"POST", "/records", body(nil)},
		{"PUT", "/records/00000000-0000-0000-0000-000000000001", body(nil)},
		{"DELETE", "/records/00000000-0000-0000-0000-000000000001", ""},
	}

	for _, r := range requests {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			store := &fakeStore{err: errors.New("БД упала")}
			rec := do(t, store, r.method, r.path, r.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("статус = %d, ожидали 500", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "БД упала") {
				t.Errorf("текст внутренней ошибки утёк клиенту: %s", rec.Body)
			}
		})
	}
}

func TestListEmptyDataIsArray(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/records", "")
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("пустой список должен быть \"data\":[], тело: %s", rec.Body)
	}
}

func TestListResponseFormat(t *testing.T) {
	store := newFakeStore("первая", "вторая", "третья")
	rec := do(t, store, "GET", "/records?fullText=вто&id="+uid(2)+","+uid(3)+"&employeeId="+uid(1)+"&status=scheduled,completed&dateStart=2026-09-27&dateEnd=2026-09-27&limit=2&offset=1", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("статус = %d; тело: %s", rec.Code, rec.Body)
	}
	var got struct {
		Success bool `json:"success"`
		Filters struct {
			FullText   struct{ Value *string }  `json:"fullText"`
			ID         struct{ Value []string } `json:"id"`
			EmployeeID struct{ Value []string } `json:"employeeId"`
			Status     struct{ Value []string } `json:"status"`
			DateStart  struct{ Value *string }  `json:"dateStart"`
			DateEnd    struct{ Value *string }  `json:"dateEnd"`
		} `json:"filters"`
		Count      int        `json:"count"`
		Pagination httpx.Page `json:"pagination"`
		Data       []Record   `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if !got.Success || got.Count != 3 || len(got.Data) != 2 || got.Data[0].Name != "вторая" {
		t.Errorf("success=%v count=%d data=%+v", got.Success, got.Count, got.Data)
	}
	if got.Pagination != (httpx.Page{Limit: 2, Offset: 1}) {
		t.Errorf("pagination = %+v", got.Pagination)
	}
	f := got.Filters
	if f.FullText.Value == nil || *f.FullText.Value != "вто" {
		t.Errorf("fullText = %v", f.FullText.Value)
	}
	if len(f.ID.Value) != 2 || f.ID.Value[0] != uid(2) || f.ID.Value[1] != uid(3) {
		t.Errorf("id = %v", f.ID.Value)
	}
	if len(f.EmployeeID.Value) != 1 || f.EmployeeID.Value[0] != uid(1) {
		t.Errorf("employeeId = %v", f.EmployeeID.Value)
	}
	if fmt.Sprint(f.Status.Value) != "[scheduled completed]" {
		t.Errorf("status = %v", f.Status.Value)
	}
	if f.DateStart.Value == nil || *f.DateStart.Value != "2026-09-27T00:00:00Z" {
		t.Errorf("dateStart = %v", f.DateStart.Value)
	}
	if f.DateEnd.Value == nil || *f.DateEnd.Value != "2026-09-27T23:59:59Z" {
		t.Errorf("dateEnd = %v", f.DateEnd.Value)
	}
}

func TestListUnsetFiltersAreNull(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/records", "")
	body := rec.Body.String()
	for _, want := range []string{
		`"fullText":{"value":null,"fields":null}`,
		`"id":{"value":null}`,
		`"employeeId":{"value":null}`,
		`"clientId":{"value":null}`,
		`"status":{"value":null}`,
		`"dateStart":{"value":null}`,
		`"dateEnd":{"value":null}`,
		`"pagination":{"limit":20,"offset":0}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в ответе нет %s\nтело: %s", want, body)
		}
	}
}

func TestListBadFilter(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/records?limit=1000", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("статус = %d, ожидали 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_query"`) || !strings.Contains(rec.Body.String(), "limit") {
		t.Errorf("тело = %s", rec.Body)
	}
}

func TestErrorsAreJSON(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/records/00000000-0000-0000-0000-000000000999", "")
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"success":false,"code":"record_not_found","error":"запись не найдена"}` {
		t.Errorf("тело = %s", got)
	}
}

func TestCreateReturnsRecord(t *testing.T) {
	store := newFakeStore()
	rec := do(t, store, "POST", "/records", body(m{"description": "описание"}))

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var got Record
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	wantStart := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	if got.ID != uid(1) || got.Name != "новая" || got.Description != "описание" ||
		got.ClientID != uid(1) || got.EmployeeID != uid(1) || got.Status != StatusScheduled ||
		!got.StartsAt.Equal(wantStart) || !got.EndsAt.Equal(wantStart.Add(time.Hour)) {
		t.Errorf("получили %+v", got)
	}
	if len(store.records) != 1 {
		t.Errorf("в хранилище %d записей, ожидали 1", len(store.records))
	}
}

func TestCreateTimeWithZone(t *testing.T) {
	store := newFakeStore()
	do(t, store, "POST", "/records", body(m{"starts_at": "2026-10-17T15:00:00+03:00", "ends_at": "2026-10-17T16:00:00+03:00"}))
	if got := store.records[0].StartsAt; !got.Equal(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("starts_at = %v, ожидали 12:00 UTC", got)
	}
}

func TestCreateTrimsName(t *testing.T) {
	store := newFakeStore()
	do(t, store, "POST", "/records", body(m{"name": "  новая  "}))
	if got := store.records[0].Name; got != "новая" {
		t.Errorf("name = %q, ожидали без пробелов по краям", got)
	}
}

func TestUpdateChangesFields(t *testing.T) {
	store := newFakeStore("старая")
	do(t, store, "PUT", "/records/00000000-0000-0000-0000-000000000001", body(m{"description": "описание", "status": "no_show"}))

	if got := store.records[0]; got.Name != "новая" || got.Description != "описание" || got.Status != StatusNoShow {
		t.Errorf("после изменения %+v", got)
	}
}

func TestDeleteRemovesRecord(t *testing.T) {
	store := newFakeStore("первая", "вторая")
	do(t, store, "DELETE", "/records/00000000-0000-0000-0000-000000000001", "")

	if len(store.records) != 1 || store.records[0].ID != uid(2) {
		t.Errorf("после удаления осталось %+v", store.records)
	}
}

func TestStoreErrorsMapToFieldsAndCodes(t *testing.T) {
	tests := []struct {
		name, body, want string
	}{
		{"нет клиента", body(m{"client_id": uid(999)}), `"fields":{"client_id":"клиент не найден"}`},
		{"нет сотрудника", body(m{"employee_id": uid(999)}), `"fields":{"employee_id":"сотрудник не найден"}`},
		{"мастер занят", body(m{"starts_at": "2026-10-16T10:00:00", "ends_at": "2026-10-16T10:15:00"}), `"code":"employee_busy"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newFakeStore("первая"), "POST", "/records", tt.body)
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("тело = %s, ожидали %s", rec.Body, tt.want)
			}
		})
	}
}

func TestValidationReportsAllFields(t *testing.T) {
	rec := do(t, newFakeStore(), "POST", "/records", `{"name":"","client_id":"abc"}`)
	var got struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "client_id", "employee_id", "starts_at", "ends_at"} {
		if got.Fields[field] == "" {
			t.Errorf("нет ошибки поля %s; ответ %+v", field, got)
		}
	}
	if got.Code != "validation_error" {
		t.Errorf("code = %q", got.Code)
	}
}
