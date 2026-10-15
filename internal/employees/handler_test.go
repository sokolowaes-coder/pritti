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
)

type fakeStore struct {
	employees  []Employee
	nextID     int
	hasRecords map[string]bool // у каких сотрудников есть записи — их нельзя удалить
	err        error
}

func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{nextID: 1, hasRecords: map[string]bool{}}
	for _, n := range names {
		s.Create(context.Background(), EmployeeInput{Name: n})
	}
	return s
}

func (s *fakeStore) List(ctx context.Context) ([]Employee, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]Employee, len(s.employees))
	copy(out, s.employees)
	return out, nil
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
