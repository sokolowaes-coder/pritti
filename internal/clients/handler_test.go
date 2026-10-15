package clients

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
	clients    []Client
	nextID     int
	hasRecords map[string]bool // у каких клиентов есть записи — их нельзя удалить
	err        error
}

func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{nextID: 1, hasRecords: map[string]bool{}}
	for i, n := range names {
		s.Create(context.Background(), ClientInput{FullName: n, Phone: fmt.Sprintf("+7916000000%d", i)})
	}
	return s
}

func (s *fakeStore) phoneTaken(id string, phone string) bool {
	for _, c := range s.clients {
		if c.Phone == phone && c.ID != id {
			return true
		}
	}
	return false
}

func (s *fakeStore) List(ctx context.Context, limit, offset int) ([]Client, int, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	start := min(offset, len(s.clients))
	end := min(start+limit, len(s.clients))
	out := make([]Client, end-start)
	copy(out, s.clients[start:end])
	return out, len(s.clients), nil
}

func (s *fakeStore) GetByID(ctx context.Context, id string) (Client, error) {
	if s.err != nil {
		return Client{}, s.err
	}
	for _, x := range s.clients {
		if x.ID == id {
			return x, nil
		}
	}
	return Client{}, ErrNotFound
}

func (s *fakeStore) Create(ctx context.Context, in ClientInput) (Client, error) {
	if s.err != nil {
		return Client{}, s.err
	}
	if s.phoneTaken("", in.Phone) {
		return Client{}, ErrPhoneTaken
	}
	c := Client{ID: uid(s.nextID), FullName: in.FullName, Phone: in.Phone, CreatedAt: time.Now()}
	s.nextID++
	s.clients = append(s.clients, c)
	return c, nil
}

func (s *fakeStore) Update(ctx context.Context, id string, in ClientInput) (Client, error) {
	if s.err != nil {
		return Client{}, s.err
	}
	for i := range s.clients {
		if s.clients[i].ID == id {
			if s.phoneTaken(id, in.Phone) {
				return Client{}, ErrPhoneTaken
			}
			s.clients[i].FullName = in.FullName
			s.clients[i].Phone = in.Phone
			return s.clients[i], nil
		}
	}
	return Client{}, ErrNotFound
}

func (s *fakeStore) Delete(ctx context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	if s.hasRecords[id] {
		return ErrHasRecords
	}
	for i, c := range s.clients {
		if c.ID == id {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// uid — n-й тестовый id: 00000000-0000-0000-0000-000000000001 и т. д.
func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func do(t *testing.T, store ClientStore, method, path, body string) *httptest.ResponseRecorder {
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
		{"список", "GET", "/clients", "", http.StatusOK},
		{"список: страница", "GET", "/clients?limit=1&offset=1", "", http.StatusOK},
		{"список: limit больше максимума", "GET", "/clients?limit=101", "", http.StatusBadRequest},
		{"список: offset отрицательный", "GET", "/clients?offset=-1", "", http.StatusBadRequest},

		{"по id", "GET", "/clients/00000000-0000-0000-0000-000000000001", "", http.StatusOK},
		{"по id: нет такого", "GET", "/clients/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"по id: id не UUID", "GET", "/clients/abc", "", http.StatusBadRequest},

		{"создание", "POST", "/clients", `{"full_name":"Иванова Анна","phone":"+79161234567"}`, http.StatusCreated},
		{"создание: телефон с 8 и скобками", "POST", "/clients", `{"full_name":"Иванова Анна","phone":"8 (916) 123-45-67"}`, http.StatusCreated},
		{"создание: пустое ФИО", "POST", "/clients", `{"full_name":"  ","phone":"+79161234567"}`, http.StatusBadRequest},
		{"создание: без full_name", "POST", "/clients", `{"phone":"+79161234567"}`, http.StatusBadRequest},
		{"создание: без phone", "POST", "/clients", `{"full_name":"Иванова Анна"}`, http.StatusBadRequest},
		{"создание: неверный phone", "POST", "/clients", `{"full_name":"Иванова Анна","phone":"12345"}`, http.StatusBadRequest},
		{"создание: телефон занят", "POST", "/clients", `{"full_name":"Иванова Анна","phone":"+79160000000"}`, http.StatusConflict},
		{"создание: сломанный JSON", "POST", "/clients", `{`, http.StatusBadRequest},

		{"изменение", "PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"Петрова Ольга","phone":"+79160000000"}`, http.StatusOK},
		{"изменение: нет клиента", "PUT", "/clients/00000000-0000-0000-0000-000000000999", `{"full_name":"x","phone":"+79161234567"}`, http.StatusNotFound},
		{"изменение: пустое ФИО", "PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"","phone":"+79161234567"}`, http.StatusBadRequest},
		{"изменение: без phone", "PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"x"}`, http.StatusBadRequest},
		{"изменение: телефон чужой", "PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"x","phone":"+79160000001"}`, http.StatusConflict},
		{"изменение: id не UUID", "PUT", "/clients/abc", `{"full_name":"x","phone":"+79161234567"}`, http.StatusBadRequest},

		{"удаление", "DELETE", "/clients/00000000-0000-0000-0000-000000000001", "", http.StatusNoContent},
		{"удаление: есть записи", "DELETE", "/clients/00000000-0000-0000-0000-000000000002", "", http.StatusConflict},
		{"удаление: нет клиента", "DELETE", "/clients/00000000-0000-0000-0000-000000000999", "", http.StatusNotFound},
		{"удаление: id не UUID", "DELETE", "/clients/abc", "", http.StatusBadRequest},
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
		{"GET", "/clients", ""},
		{"GET", "/clients/00000000-0000-0000-0000-000000000001", ""},
		{"POST", "/clients", `{"full_name":"x","phone":"+79161234567"}`},
		{"PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"x","phone":"+79161234567"}`},
		{"DELETE", "/clients/00000000-0000-0000-0000-000000000001", ""},
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
	rec := do(t, newFakeStore("Анна", "Борис"), "GET", "/clients", "")
	var got struct {
		Success bool     `json:"success"`
		Count   int      `json:"count"`
		Data    []Client `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Success || got.Count != 2 || len(got.Data) != 2 || got.Data[1].FullName != "Борис" {
		t.Errorf("получили %+v", got)
	}
}

func TestListPagination(t *testing.T) {
	rec := do(t, newFakeStore("Анна", "Борис", "Вера"), "GET", "/clients?limit=1&offset=1", "")
	var got httpx.ListResponse[Client]
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 3 || len(got.Data) != 1 || got.Data[0].ID != uid(2) || got.Pagination != (httpx.Page{Limit: 1, Offset: 1}) {
		t.Errorf("получили %+v", got)
	}
}

func TestListEmptyDataIsArray(t *testing.T) {
	rec := do(t, newFakeStore(), "GET", "/clients", "")
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("пустой список должен быть \"data\":[], тело: %s", rec.Body)
	}
}

func TestCreateNormalizesPhone(t *testing.T) {
	store := newFakeStore()
	rec := do(t, store, "POST", "/clients", `{"full_name":" Иванова Анна ","phone":"8 (916) 123-45-67"}`)
	var got Client
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if got.ID != uid(1) || got.FullName != "Иванова Анна" || got.Phone != "+79161234567" {
		t.Errorf("создали %+v", got)
	}

	do(t, store, "PUT", "/clients/00000000-0000-0000-0000-000000000001", `{"full_name":"Петрова Ольга","phone":"+7 999 000-11-22"}`)
	if c := store.clients[0]; c.FullName != "Петрова Ольга" || c.Phone != "+79990001122" {
		t.Errorf("после изменения %+v", c)
	}
}

func TestValidationReportsAllFields(t *testing.T) {
	rec := do(t, newFakeStore(), "POST", "/clients", `{"full_name":" ","phone":"123"}`)
	var got struct {
		Code   string            `json:"code"`
		Fields map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || got.Code != "validation_error" || got.Fields["full_name"] == "" || got.Fields["phone"] == "" {
		t.Errorf("статус %d, ответ %+v; ожидали ошибки обоих полей сразу", rec.Code, got)
	}
}

func TestErrorCodes(t *testing.T) {
	store := newFakeStore("Анна")
	store.hasRecords[uid(1)] = true
	tests := []struct{ method, path, body, wantCode string }{
		{"GET", "/clients/00000000-0000-0000-0000-000000000999", "", "client_not_found"},
		{"POST", "/clients", `{"full_name":"x","phone":"+79160000000"}`, "phone_taken"},
		{"DELETE", "/clients/00000000-0000-0000-0000-000000000001", "", "client_has_records"},
		{"GET", "/clients/abc", "", "invalid_id"},
		{"GET", "/clients?limit=0", "", "invalid_query"},
	}
	for _, tt := range tests {
		rec := do(t, store, tt.method, tt.path, tt.body)
		if !strings.Contains(rec.Body.String(), `"code":"`+tt.wantCode+`"`) {
			t.Errorf("%s %s: тело %s, ожидали код %s", tt.method, tt.path, rec.Body, tt.wantCode)
		}
	}
}
