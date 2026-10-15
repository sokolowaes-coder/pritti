package records

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"pritti/internal/testdb"
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
	return clientID, newEmployee(t, s, "сотрудник")
}

func newEmployee(t *testing.T, s *Storage, name string) string {
	t.Helper()
	var id string
	if err := s.db.QueryRow(context.Background(), `INSERT INTO employees (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// at — время 16.10.2026 в UTC: at(10, 30) — 10:30.
func at(hour, minute int) time.Time {
	return time.Date(2026, 10, 16, hour, minute, 0, 0, time.UTC)
}

// visit — запись с часу from до часу to 16.10.2026.
func visit(name, clientID, employeeID string, from, to int) RecordInput {
	return RecordInput{Name: name, ClientID: clientID, EmployeeID: employeeID,
		StartsAt: at(from, 0), EndsAt: at(to, 0), Status: StatusScheduled}
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
	in := visit("первая", clientID, employeeID, 10, 11)
	in.Description = "описание"
	created, err := s.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "первая" || created.Description != "описание" || created.CreatedAt.IsZero() ||
		created.ClientID != clientID || created.EmployeeID != employeeID || created.Status != StatusScheduled ||
		!created.StartsAt.Equal(at(10, 0)) || !created.EndsAt.Equal(at(11, 0)) {
		t.Errorf("Create вернул %+v", created)
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(got, created) {
		t.Errorf("GetByID: %+v, %v", got, err)
	}

	in = visit("изменённая", clientID, employeeID, 12, 13)
	in.Status = StatusCompleted
	updated, err := s.Update(ctx, created.ID, in)
	if err != nil || updated.Name != "изменённая" || updated.Description != "" || updated.ID != created.ID ||
		updated.Status != StatusCompleted || !updated.StartsAt.Equal(at(12, 0)) {
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
	if _, err := s.Update(ctx, missingID, visit("x", clientID, employeeID, 10, 11)); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v, ожидали ErrNotFound", err)
	}
	if err := s.Delete(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, ожидали ErrNotFound", err)
	}
}

// Проверки, которые держит сама БД, даже если Go-код их пропустит.
func TestStorageRejectsInvalidData(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)

	blank := visit("   ", clientID, employeeID, 10, 11)
	backwards := visit("x", clientID, employeeID, 11, 10)
	badStatus := visit("x", clientID, employeeID, 10, 11)
	badStatus.Status = "done"

	for name, in := range map[string]RecordInput{"пустое имя": blank, "конец раньше начала": backwards, "неизвестный статус": badStatus} {
		if _, err := s.Create(ctx, in); err == nil {
			t.Errorf("%s: БД приняла запись, ожидали ошибку CHECK", name)
		}
	}
}

func TestStorageUnknownClientOrEmployee(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)

	if _, err := s.Create(ctx, visit("x", missingID, employeeID, 10, 11)); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("Create: %v, ожидали ErrClientNotFound", err)
	}
	if _, err := s.Create(ctx, visit("x", clientID, missingID, 10, 11)); !errors.Is(err, ErrEmployeeNotFound) {
		t.Errorf("Create: %v, ожидали ErrEmployeeNotFound", err)
	}

	rec, err := s.Create(ctx, visit("x", clientID, employeeID, 10, 11))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, rec.ID, visit("x", missingID, employeeID, 10, 11)); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("Update: %v, ожидали ErrClientNotFound", err)
	}
}

func TestStorageEmployeeOverlap(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)
	otherEmployee := newEmployee(t, s, "другой мастер")

	first, err := s.Create(ctx, visit("10–12", clientID, employeeID, 10, 12))
	if err != nil {
		t.Fatal(err)
	}

	cancelled := visit("отменённая 11–13", clientID, employeeID, 11, 13)
	cancelled.Status = StatusCancelled

	tests := []struct {
		name    string
		in      RecordInput
		wantErr error
	}{
		{"пересечение с концом", visit("11–13", clientID, employeeID, 11, 13), ErrEmployeeBusy},
		{"внутри чужой записи", RecordInput{Name: "10:30–11:00", ClientID: clientID, EmployeeID: employeeID,
			StartsAt: at(10, 30), EndsAt: at(11, 0), Status: StatusScheduled}, ErrEmployeeBusy},
		{"накрывает чужую запись", visit("9–13", clientID, employeeID, 9, 13), ErrEmployeeBusy},
		{"впритык после: [10, 12) и [12, 13) не пересекаются", visit("12–13", clientID, employeeID, 12, 13), nil},
		{"впритык до", visit("9–10", clientID, employeeID, 9, 10), nil},
		{"то же время у другого мастера", visit("10–12", clientID, otherEmployee, 10, 12), nil},
		{"отменённая не мешает", cancelled, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Create(ctx, tt.in); !errors.Is(err, tt.wantErr) {
				t.Errorf("Create: %v, ожидали %v", err, tt.wantErr)
			}
		})
	}

	// Перенос своей записи на пересекающееся с ней же время — не конфликт.
	if _, err := s.Update(ctx, first.ID, visit("10–11", clientID, employeeID, 10, 11)); err != nil {
		t.Errorf("Update своей записи: %v", err)
	}
	// Первую сократили до 10–11 — время 11–12 освободилось.
	if _, err := s.Create(ctx, RecordInput{Name: "11–12", ClientID: clientID, EmployeeID: employeeID,
		StartsAt: at(11, 0), EndsAt: at(12, 0), Status: StatusScheduled}); err != nil {
		t.Errorf("Create на освободившееся время: %v", err)
	}
}

// Главное преимущество ограничения в БД: из параллельных запросов на одно время
// проходит ровно один. Проверка «время свободно?» в Go пропустила бы несколько.
func TestStorageEmployeeOverlapConcurrent(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create(ctx, visit(fmt.Sprintf("попытка %d", i), clientID, employeeID, 10, 11))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	var ok, busy int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrEmployeeBusy):
			busy++
		default:
			t.Errorf("неожиданная ошибка: %v", err)
		}
	}
	if ok != 1 || busy != n-1 {
		t.Errorf("создано %d, отказано %d; ожидали 1 и %d", ok, busy, n-1)
	}
}

func TestStorageListFilters(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	clientID, employeeID := newRefs(t, s)
	otherEmployee := newEmployee(t, s, "другой мастер")
	var otherClient string
	if err := s.db.QueryRow(ctx, `INSERT INTO clients (full_name, phone) VALUES ('другой клиент', '+79990001122') RETURNING id`).Scan(&otherClient); err != nil {
		t.Fatal(err)
	}

	// Записи создаются не по порядку времени: список должен сортироваться по starts_at.
	// Время начала у всех разное: при равном starts_at порядок решает id, а UUID v7,
	// созданные в одну миллисекунду, различаются случайными битами — порядок был бы случайным.
	ids := map[string]string{} // имя → id
	for _, in := range []RecordInput{
		visit("Чистка лица", clientID, employeeID, 11, 12),
		visit("Лазерная депиляция", clientID, employeeID, 10, 11),
		{Name: "Массаж", ClientID: otherClient, EmployeeID: otherEmployee, StartsAt: at(10, 30), EndsAt: at(11, 30), Status: StatusScheduled},
		visit("Массаж_спины", clientID, employeeID, 12, 13),
		{Name: "Пилинг", ClientID: clientID, EmployeeID: employeeID,
			StartsAt: time.Date(2020, 1, 15, 12, 0, 0, 0, time.UTC), EndsAt: time.Date(2020, 1, 15, 13, 0, 0, 0, time.UTC), Status: StatusCompleted},
	} {
		switch in.Name {
		case "Лазерная депиляция":
			in.Description = "кабинет 1"
		case "Чистка лица":
			in.Description = "после лазерной процедуры"
		case "Массаж":
			in.Description = "скидка 50%"
		}
		rec, err := s.Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		ids[rec.Name] = rec.ID
	}

	str := func(v string) *string { return &v }
	tm := func(v string) *time.Time {
		parsed, _ := time.Parse(time.RFC3339, v)
		return &parsed
	}

	tests := []struct {
		name      string
		filter    ListFilter
		wantNames []string
		wantTotal int
	}{
		{"без фильтров, по времени визита", ListFilter{}, []string{"Пилинг", "Лазерная депиляция", "Массаж", "Чистка лица", "Массаж_спины"}, 5},
		{"поиск без учёта регистра по name и description", ListFilter{FullText: str("ЛАЗЕР")}, []string{"Лазерная депиляция", "Чистка лица"}, 2},
		{"поиск только по name", ListFilter{FullText: str("лазер"), FullTextFields: []string{"name"}}, []string{"Лазерная депиляция"}, 1},
		{"% ищется как символ", ListFilter{FullText: str("50%")}, []string{"Массаж"}, 1},
		{"_ ищется как символ", ListFilter{FullText: str("_")}, []string{"Массаж_спины"}, 1},
		{"по списку id", ListFilter{IDs: []string{ids["Чистка лица"], ids["Массаж_спины"], missingID}}, []string{"Чистка лица", "Массаж_спины"}, 2},
		{"по мастеру", ListFilter{EmployeeIDs: []string{otherEmployee}}, []string{"Массаж"}, 1},
		{"по клиенту", ListFilter{ClientIDs: []string{otherClient}}, []string{"Массаж"}, 1},
		{"по статусу", ListFilter{Statuses: []string{StatusCompleted}}, []string{"Пилинг"}, 1},
		{"dateEnd в прошлом", ListFilter{DateEnd: tm("2020-12-31T00:00:00Z")}, []string{"Пилинг"}, 1},
		{"окно по времени визита", ListFilter{DateStart: tm("2026-10-16T10:45:00Z"), DateEnd: tm("2026-10-16T12:00:00Z")}, []string{"Чистка лица", "Массаж_спины"}, 2},
		{"фильтры вместе (AND)", ListFilter{FullText: str("массаж"), EmployeeIDs: []string{employeeID}}, []string{"Массаж_спины"}, 1},
		{"пагинация: count — всего, data — страница", ListFilter{Limit: 2, Offset: 1}, []string{"Лазерная депиляция", "Массаж"}, 5},
		{"offset за концом", ListFilter{Limit: 2, Offset: 10}, []string{}, 5},
		{"offset за концом с фильтром", ListFilter{FullText: str("лазер"), Limit: 2, Offset: 10}, []string{}, 2},
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
