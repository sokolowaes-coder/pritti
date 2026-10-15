package employees

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

type fakeStore struct {
	employees  []Employee
	nextID     int
	hasRecords map[string]bool // у каких сотрудников есть записи — их нельзя удалить
	hours      map[string][]WorkingDay
	err        error
}

func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{nextID: 1, hasRecords: map[string]bool{}}
	for _, n := range names {
		s.Create(context.Background(), EmployeeInput{Name: n})
	}
	return s
}

func (s *fakeStore) List(ctx context.Context, limit, offset int) ([]Employee, int, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	start := min(offset, len(s.employees))
	end := min(start+limit, len(s.employees))
	out := make([]Employee, end-start)
	copy(out, s.employees[start:end])
	return out, len(s.employees), nil
}

func (s *fakeStore) GetByID(ctx context.Context, id string) (Employee, error) {
	if s.err != nil {
		return Employee{}, s.err
	}
	for _, x := range s.employees {
		if x.ID == id {
			return x, nil
		}
	}
	return Employee{}, ErrNotFound
}

func (s *fakeStore) Create(ctx context.Context, in EmployeeInput) (Employee, error) {
	if s.err != nil {
		return Employee{}, s.err
	}
	e := Employee{ID: uid(s.nextID), Name: in.Name, CreatedAt: time.Now()}
	s.nextID++
	s.employees = append(s.employees, e)
	return e, nil
}

func (s *fakeStore) Update(ctx context.Context, id string, in EmployeeInput) (Employee, error) {
	if s.err != nil {
		return Employee{}, s.err
	}
	for i := range s.employees {
		if s.employees[i].ID == id {
			s.employees[i].Name = in.Name
			return s.employees[i], nil
		}
	}
	return Employee{}, ErrNotFound
}

func (s *fakeStore) Delete(ctx context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	if s.hasRecords[id] {
		return ErrHasRecords
	}
	for i, e := range s.employees {
		if e.ID == id {
			s.employees = append(s.employees[:i], s.employees[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (s *fakeStore) WorkingHours(ctx context.Context, employeeID string) ([]WorkingDay, error) {
	if s.err != nil {
		return nil, s.err
	}
	if _, err := s.GetByID(ctx, employeeID); err != nil {
		return nil, err
	}
	return append([]WorkingDay{}, s.hours[employeeID]...), nil
}

func (s *fakeStore) SetWorkingHours(ctx context.Context, employeeID string, days []WorkingDay) error {
	if _, err := s.WorkingHours(ctx, employeeID); err != nil {
		return err
	}
	if s.hours == nil {
		s.hours = map[string][]WorkingDay{}
	}
	s.hours[employeeID] = days
	return nil
}

// uid — n-й тестовый id: 00000000-0000-0000-0000-000000000001 и т. д.
func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func do(t *testing.T, store EmployeeStore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(store).Register(mux)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"список", "GET", "/employees", "", http.StatusOK},
		{"список: страница", "GET", "/employees?limit=1&offset=1", "", http.StatusOK},
		{"список: limit больше максимума", "GET", "/employees?limit=101", "", http.StatusBadRequest},
		{"список: offset отрицательный", "GET", "/employees?offset=-1", "", http.StatusBadRequest},

		{"по id", "GET", "/employees/00000000-0000-0000-0000-000000000001", "", http.StatusOK},
		{"по id: нет такого", "GET", "/employees/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"по id: id не UUID", "GET", "/employees/abc", "", http.StatusBadRequest},

		{"создание", "POST", "/employees", `{"name":"Анна"}`, http.StatusCreated},
		{"создание: пустое имя", "POST", "/employees", `{"name":"  "}`, http.StatusBadRequest},
		{"создание: без name", "POST", "/employees", `{}`, http.StatusBadRequest},
		{"создание: сломанный JSON", "POST", "/employees", `{`, http.StatusBadRequest},

		{"изменение", "PUT", "/employees/00000000-0000-0000-0000-000000000001", `{"name":"Ольга"}`, http.StatusOK},
		{"изменение: нет сотрудника", "PUT", "/employees/00000000-0000-0000-0000-000000000999", `{"name":"x"}`, http.StatusNotFound},
		{"изменение: пустое имя", "PUT", "/employees/00000000-0000-0000-0000-000000000001", `{"name":""}`, http.StatusBadRequest},
		{"изменение: id не UUID", "PUT", "/employees/abc", `{"name":"x"}`, http.StatusBadRequest},

		{"удаление", "DELETE", "/employees/00000000-0000-0000-0000-000000000001", "", http.StatusNoContent},
		{"удаление: есть записи", "DELETE", "/employees/00000000-0000-0000-0000-000000000002", "", http.StatusConflict},
		{"удаление: нет сотрудника", "DELETE", "/employees/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"удаление: id не UUID", "DELETE", "/employees/abc", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore("Анна", "Борис")
			store.hasRecords[uid(2)] = true
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
		{"GET", "/employees", ""},
		{"GET", "/employees/00000000-0000-0000-0000-000000000001", ""},
		{"POST", "/employees", `{"name":"x"}`},
		{"PUT", "/employees/00000000-0000-0000-0000-000000000001", `{"name":"x"}`},
		{"DELETE", "/employees/00000000-0000-0000-0000-000000000001", ""},
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

func TestListResponseFormat(t *testing.T) {
	rec := do(t, newFakeStore("Анна", "Борис"), "GET", "/employees", "")
	var got struct {
		Success bool       `json:"success"`
		Count   int        `json:"count"`
		Data    []Employee `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Count != 2 || len(got.Data) != 2 || got.Data[1].Name != "Борис" {
		t.Errorf("получили %+v", got)
	}
}

func TestListPagination(t *testing.T) {
	rec := do(t, newFakeStore("Анна", "Борис", "Вера"), "GET", "/employees?limit=1&offset=1", "")
	var got httpx.ListResponse[Employee]
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 3 || len(got.Data) != 1 || got.Data[0].ID != uid(2) || got.Pagination != (httpx.Page{Limit: 1, Offset: 1}) {
		t.Errorf("получили %+v", got)
	}
}

func TestListEmptyDataIsArray(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/employees", "")
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("пустой список должен быть \"data\":[], тело: %s", rec.Body)
	}
}

func TestCreateAndUpdate(t *testing.T) {
	store := newFakeStore()
	rec := do(t, store, "POST", "/employees", `{"name":"Анна"}`)
	var got Employee
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if got.ID != uid(1) || got.Name != "Анна" {
		t.Errorf("создали %+v", got)
	}

	do(t, store, "PUT", "/employees/00000000-0000-0000-0000-000000000001", `{"name":"Ольга"}`)
	if store.employees[0].Name != "Ольга" {
		t.Errorf("после изменения %+v", store.employees[0])
	}
}

func TestSchedule(t *testing.T) {
	const path = "/employees/00000000-0000-0000-0000-000000000001/schedule"
	tests := []struct {
		name, method, path, body string
		wantStatus               int
		wantInBody               string
	}{
		{"пустой график", "GET", path, "", 200, `"days":[]`},
		{"нет мастера", "GET", "/employees/00000000-0000-0000-0000-000000000999/schedule", "", 404, "employee_not_found"},
		{"задать", "PUT", path, `{"days":[{"weekday":1,"start":"9:00","end":"18:00"},{"weekday":6,"start":"10:00","end":"15:30"}]}`, 200,
			`"days":[{"weekday":1,"start":"09:00","end":"18:00"},{"weekday":6,"start":"10:00","end":"15:30"}]`},
		{"пустой — не работает ни дня", "PUT", path, `{"days":[]}`, 200, `"days":[]`},
		{"без days — тоже пустой", "PUT", path, `{}`, 200, `"days":[]`},
		{"день недели 0", "PUT", path, `{"days":[{"weekday":0,"start":"10:00","end":"19:00"}]}`, 400, `"days[0].weekday"`},
		{"день недели 8", "PUT", path, `{"days":[{"weekday":8,"start":"10:00","end":"19:00"}]}`, 400, `"days[0].weekday"`},
		{"день дважды", "PUT", path, `{"days":[{"weekday":1,"start":"10:00","end":"12:00"},{"weekday":1,"start":"13:00","end":"19:00"}]}`, 400, `"days[1].weekday"`},
		{"кривое время", "PUT", path, `{"days":[{"weekday":1,"start":"10","end":"25:00"}]}`, 400, `"days[0].end"`},
		{"конец раньше начала", "PUT", path, `{"days":[{"weekday":1,"start":"19:00","end":"10:00"}]}`, 400, "должно быть позже start"},
		{"нет мастера", "PUT", "/employees/00000000-0000-0000-0000-000000000999/schedule", `{"days":[]}`, 404, "employee_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newFakeStore("Анна"), tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantInBody) {
				t.Errorf("статус %d, тело %s; ожидали %d и %s", rec.Code, rec.Body, tt.wantStatus, tt.wantInBody)
			}
		})
	}
}
