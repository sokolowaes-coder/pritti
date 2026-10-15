package services

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
	services   []Service
	nextID     int
	hasRecords map[string]bool // на какие услуги есть записи — их нельзя удалить
	err        error
}

func newFakeStore(names ...string) *fakeStore {
	s := &fakeStore{nextID: 1, hasRecords: map[string]bool{}}
	for _, n := range names {
		s.Create(context.Background(), ServiceInput{Name: n, DurationMinutes: 60, PriceKopecks: 150000})
	}
	return s
}

func (s *fakeStore) nameTaken(id, name string) bool {
	for _, sv := range s.services {
		if strings.EqualFold(sv.Name, name) && sv.ID != id {
			return true
		}
	}
	return false
}

func (s *fakeStore) List(ctx context.Context, limit, offset int) ([]Service, int, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	start := min(offset, len(s.services))
	end := min(start+limit, len(s.services))
	return append([]Service{}, s.services[start:end]...), len(s.services), nil
}

func (s *fakeStore) GetByID(ctx context.Context, id string) (Service, error) {
	if s.err != nil {
		return Service{}, s.err
	}
	for _, sv := range s.services {
		if sv.ID == id {
			return sv, nil
		}
	}
	return Service{}, ErrNotFound
}

func (s *fakeStore) Create(ctx context.Context, in ServiceInput) (Service, error) {
	if s.err != nil {
		return Service{}, s.err
	}
	if s.nameTaken("", in.Name) {
		return Service{}, ErrNameTaken
	}
	sv := Service{ID: uid(s.nextID), Name: in.Name, DurationMinutes: in.DurationMinutes, PriceKopecks: in.PriceKopecks, CreatedAt: time.Now()}
	s.nextID++
	s.services = append(s.services, sv)
	return sv, nil
}

func (s *fakeStore) Update(ctx context.Context, id string, in ServiceInput) (Service, error) {
	if s.err != nil {
		return Service{}, s.err
	}
	for i := range s.services {
		if s.services[i].ID == id {
			if s.nameTaken(id, in.Name) {
				return Service{}, ErrNameTaken
			}
			s.services[i].Name, s.services[i].DurationMinutes, s.services[i].PriceKopecks = in.Name, in.DurationMinutes, in.PriceKopecks
			return s.services[i], nil
		}
	}
	return Service{}, ErrNotFound
}

func (s *fakeStore) Delete(ctx context.Context, id string) error {
	if s.err != nil {
		return s.err
	}
	if s.hasRecords[id] {
		return ErrHasRecords
	}
	for i, sv := range s.services {
		if sv.ID == id {
			s.services = append(s.services[:i], s.services[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// uid — n-й тестовый id: 00000000-0000-0000-0000-000000000001 и т. д.
func uid(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

func do(t *testing.T, store ServiceStore, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(store).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestHandlerStatusCodes(t *testing.T) {
	const one = "/services/00000000-0000-0000-0000-000000000001"
	const ok = `{"name":"Окрашивание","duration_minutes":120,"price_kopecks":350000}`
	tests := []struct {
		name, method, path, body string
		wantStatus               int
		wantCode                 string
	}{
		{"список", "GET", "/services", "", 200, ""},
		{"список: кривой limit", "GET", "/services?limit=0", "", 400, "invalid_query"},
		{"по id", "GET", one, "", 200, ""},
		{"по id: нет такой", "GET", "/services/00000000-0000-0000-0000-000000000999", "", 404, "service_not_found"},

		{"создание", "POST", "/services", ok, 201, ""},
		{"создание: бесплатная", "POST", "/services", `{"name":"Консультация","duration_minutes":15,"price_kopecks":0}`, 201, ""},
		{"создание: название занято (без учёта регистра)", "POST", "/services", `{"name":"СТРИЖКА","duration_minutes":30,"price_kopecks":1}`, 409, "service_name_taken"},
		{"создание: без названия", "POST", "/services", `{"duration_minutes":30,"price_kopecks":1}`, 400, "validation_error"},
		{"создание: длительность меньше 5", "POST", "/services", `{"name":"x","duration_minutes":4,"price_kopecks":1}`, 400, "validation_error"},
		{"создание: длительность больше 12 часов", "POST", "/services", `{"name":"x","duration_minutes":721,"price_kopecks":1}`, 400, "validation_error"},
		{"создание: цена отрицательная", "POST", "/services", `{"name":"x","duration_minutes":30,"price_kopecks":-1}`, 400, "validation_error"},
		{"создание: цена дробная", "POST", "/services", `{"name":"x","duration_minutes":30,"price_kopecks":10.5}`, 400, "invalid_json"},

		{"изменение", "PUT", one, ok, 200, ""},
		{"изменение: своё же название", "PUT", one, `{"name":"стрижка","duration_minutes":45,"price_kopecks":1}`, 200, ""},
		{"изменение: чужое название", "PUT", one, `{"name":"Маникюр","duration_minutes":45,"price_kopecks":1}`, 409, "service_name_taken"},
		{"изменение: нет такой", "PUT", "/services/00000000-0000-0000-0000-000000000999", ok, 404, "service_not_found"},

		{"удаление", "DELETE", one, "", 204, ""},
		{"удаление: есть записи", "DELETE", "/services/00000000-0000-0000-0000-000000000002", "", 409, "service_has_records"},
		{"удаление: нет такой", "DELETE", "/services/00000000-0000-0000-0000-000000000999", "", 404, "service_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore("Стрижка", "Маникюр")
			store.hasRecords[uid(2)] = true
			rec := do(t, store, tt.method, tt.path, tt.body)
			if rec.Code != tt.wantStatus || tt.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Errorf("статус %d, тело %s; ожидали %d и код %q", rec.Code, rec.Body, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestHandlerStoreError(t *testing.T) {
	store := &fakeStore{err: errors.New("БД упала")}
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/services", ""},
		{"GET", "/services/00000000-0000-0000-0000-000000000001", ""},
		{"POST", "/services", `{"name":"x","duration_minutes":30,"price_kopecks":1}`},
		{"PUT", "/services/00000000-0000-0000-0000-000000000001", `{"name":"x","duration_minutes":30,"price_kopecks":1}`},
		{"DELETE", "/services/00000000-0000-0000-0000-000000000001", ""},
	} {
		rec := do(t, store, r.method, r.path, r.body)
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "БД упала") {
			t.Errorf("%s %s: статус %d, тело %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
}

func TestCreateReturnsService(t *testing.T) {
	rec := do(t, newFakeStore(), "POST", "/services", `{"name":"  Окрашивание  ","duration_minutes":120,"price_kopecks":350000}`)
	var got Service
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Окрашивание" || got.DurationMinutes != 120 || got.PriceKopecks != 350000 || got.Duration() != 2*time.Hour {
		t.Errorf("получили %+v", got)
	}
}
