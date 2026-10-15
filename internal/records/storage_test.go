package records

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go-education/internal/testdb"
)

func newTestStorage(t *testing.T) *Storage {
	return NewStorage(testdb.New(t))
}

// newRefs создаёт клиента и сотрудника, без которых нельзя создать запись.
// Прямым SQL, чтобы тесты записей не зависели от пакетов clients и employees.
func newRefs(t *testing.T, s *Storage) (clientID, employeeID string) {
	t.Helper()
	ctx := context.Background()
	if err := s.db.QueryRow(ctx, `INSERT INTO clients (full_name, phone) VALUES ('клиент', '+79161234567') RETURNING id`).Scan(&clientID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(ctx, `INSERT INTO employees (name) VALUES ('сотрудник') RETURNING id`).Scan(&employeeID); err != nil {
		t.Fatal(err)
	}
	return clientID, employeeID
}

// missingID — валидный UUID, которого нет в базе.
const missingID = "00000000-0000-0000-0000-000000000999"

func TestStorageCRUD(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	list, total, err := s.List(ctx, ListFilter{Limit: 20})
	if err != nil || len(list) != 0 || list == nil || total != 0 {
		t.Fatalf("пустой список: %v, %d, %v", list, total, err)
	}

	clientID, employeeID := newRefs(t, s)
	created, err := s.Create(ctx, RecordInput{Name: "первая", Description: "описание", ClientID: clientID, EmployeeID: employeeID})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "первая" || created.Description != "описание" || created.CreatedAt.IsZero() ||
		created.ClientID == nil || *created.ClientID != clientID || created.EmployeeID == nil || *created.EmployeeID != employeeID {
		t.Errorf("Create вернул %+v", created)
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(got, created) {
		t.Errorf("GetByID: %+v, %v", got, err)
	}

	updated, err := s.Update(ctx, created.ID, RecordInput{Name: "изменённая", ClientID: clientID, EmployeeID: employeeID})
	if err != nil || updated.Name != "изменённая" || updated.Description != "" || updated.ID != created.ID {
		t.Errorf("Update: %+v, %v", updated, err)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
	list, _, _ = s.List(ctx, ListFilter{Limit: 20})
	if len(list) != 0 {
		t.Errorf("после удаления осталось %v", list)
	}
}

func TestStorageNotFound(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()

	if _, err := s.GetByID(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: %v, ожидали ErrNotFound", err)
	}
	clientID, employeeID := newRefs(t, s)
	if _, err := s.Update(ctx, missingID, RecordInput{Name: "x", ClientID: clientID, EmployeeID: employeeID}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v, ожидали ErrNotFound", err)
	}
	if err := s.Delete(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, ожидали ErrNotFound", err)
	}
}

func TestStorageRejectsBlankName(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)
	if _, err := s.Create(ctx, RecordInput{Name: "   ", ClientID: clientID, EmployeeID: employeeID}); err == nil {
		t.Error("БД приняла пустое имя, ожидали ошибку CHECK")
	}
}

func TestStorageUnknownClientOrEmployee(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)

	if _, err := s.Create(ctx, RecordInput{Name: "x", ClientID: missingID, EmployeeID: employeeID}); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("Create: %v, ожидали ErrClientNotFound", err)
	}
	if _, err := s.Create(ctx, RecordInput{Name: "x", ClientID: clientID, EmployeeID: missingID}); !errors.Is(err, ErrEmployeeNotFound) {
		t.Errorf("Create: %v, ожидали ErrEmployeeNotFound", err)
	}

	rec, err := s.Create(ctx, RecordInput{Name: "x", ClientID: clientID, EmployeeID: employeeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, rec.ID, RecordInput{Name: "x", ClientID: missingID, EmployeeID: employeeID}); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("Update: %v, ожидали ErrClientNotFound", err)
	}
}

func TestStorageListFilters(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)

	ids := map[string]string{} // имя → id
	for _, in := range []RecordInput{
		{Name: "Лазерная депиляция", Description: "кабинет 1"},
		{Name: "Чистка лица", Description: "после лазерной процедуры"},
		{Name: "Массаж", Description: "скидка 50%"},
		{Name: "Массаж_спины"},
		{Name: "Пилинг"},
	} {
		in.ClientID, in.EmployeeID = clientID, employeeID
		rec, err := s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids[rec.Name] = rec.ID
	}
	// created_at у всех «сейчас»; одну запись сдвигаем в прошлое для фильтра по дате.
	// Список сортируется по created_at, поэтому «Пилинг» дальше везде первая.
	if _, err := s.db.Exec(ctx, `UPDATE records SET created_at = '2020-01-15T12:00:00Z' WHERE name = 'Пилинг'`); err != nil {
		t.Fatal(err)
	}

	str := func(v string) *string { return &v }
	at := func(v string) *time.Time {
		tm, _ := time.Parse(time.RFC3339, v)
		return &tm
	}

	tests := []struct {
		name      string
		filter    ListFilter
		wantNames []string
		wantTotal int
	}{
		{"без фильтров, от старых к новым", ListFilter{}, []string{"Пилинг", "Лазерная депиляция", "Чистка лица", "Массаж", "Массаж_спины"}, 5},
		{"поиск без учёта регистра по name и description", ListFilter{FullText: str("ЛАЗЕР")}, []string{"Лазерная депиляция", "Чистка лица"}, 2},
		{"поиск только по name", ListFilter{FullText: str("лазер"), FullTextFields: []string{"name"}}, []string{"Лазерная депиляция"}, 1},
		{"% ищется как символ", ListFilter{FullText: str("50%")}, []string{"Массаж"}, 1},
		{"_ ищется как символ", ListFilter{FullText: str("_")}, []string{"Массаж_спины"}, 1},
		{"по списку id", ListFilter{IDs: []string{ids["Чистка лица"], ids["Массаж_спины"], missingID}}, []string{"Чистка лица", "Массаж_спины"}, 2},
		{"dateEnd в прошлом", ListFilter{DateEnd: at("2020-12-31T00:00:00Z")}, []string{"Пилинг"}, 1},
		{"dateStart отсекает старую", ListFilter{DateStart: at("2021-01-01T00:00:00Z")}, []string{"Лазерная депиляция", "Чистка лица", "Массаж", "Массаж_спины"}, 4},
		{"фильтры вместе (AND)", ListFilter{FullText: str("массаж"), IDs: []string{ids["Массаж"], ids["Пилинг"]}}, []string{"Массаж"}, 1},
		{"пагинация: count — всего, data — страница", ListFilter{Limit: 2, Offset: 1}, []string{"Лазерная депиляция", "Чистка лица"}, 5},
		{"offset за концом", ListFilter{Limit: 2, Offset: 10}, []string{}, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.filter.Limit == 0 {
				tt.filter.Limit = 20
			}
			list, total, err := s.List(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, r := range list {
				names = append(names, r.Name)
			}
			if fmt.Sprint(names) != fmt.Sprint(tt.wantNames) || total != tt.wantTotal {
				t.Errorf("получили %v (всего %d), ожидали %v (всего %d)", names, total, tt.wantNames, tt.wantTotal)
			}
		})
	}
}
