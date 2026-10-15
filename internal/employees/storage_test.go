package employees

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"pritti/internal/testdb"
)

// missingID — валидный UUID, которого нет в базе.
const missingID = "00000000-0000-0000-0000-000000000999"

func TestStorageCRUD(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	list, total, err := s.List(ctx, 20, 0)
	if err != nil || list == nil || len(list) != 0 || total != 0 {
		t.Fatalf("пустой список: %v, %d, %v", list, total, err)
	}

	created, err := s.Create(ctx, EmployeeInput{Name: "Анна"})
	if err != nil || created.ID == "" || created.Name != "Анна" || created.CreatedAt.IsZero() {
		t.Fatalf("Create: %+v, %v", created, err)
	}

	updated, err := s.Update(ctx, created.ID, EmployeeInput{Name: "Ольга"})
	if err != nil || updated.ID != created.ID || updated.Name != "Ольга" {
		t.Errorf("Update: %+v, %v", updated, err)
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil || got != updated {
		t.Errorf("GetByID: %+v, %v", got, err)
	}

	list, total, err = s.List(ctx, 20, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].Name != "Ольга" {
		t.Errorf("List: %+v, %v", list, err)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if list, _, _ = s.List(ctx, 20, 0); len(list) != 0 {
		t.Errorf("после удаления осталось %v", list)
	}
}

func TestStorageNotFound(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()

	if _, err := s.Update(ctx, missingID, EmployeeInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v, ожидали ErrNotFound", err)
	}
	if err := s.Delete(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, ожидали ErrNotFound", err)
	}
	if _, err := s.GetByID(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: %v, ожидали ErrNotFound", err)
	}
}

func TestStorageRejectsBlankName(t *testing.T) {
	s := NewStorage(testdb.New(t))
	if _, err := s.Create(context.Background(), EmployeeInput{Name: "   "}); err == nil {
		t.Error("БД приняла пустое имя, ожидали ошибку CHECK")
	}
}

func TestStorageDeleteWithRecords(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	e, err := s.Create(ctx, EmployeeInput{Name: "Анна"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		WITH c AS (INSERT INTO clients (full_name, phone) VALUES ('Иванова Анна', '+79161234567') RETURNING id)
		INSERT INTO records (name, client_id, employee_id, starts_at, ends_at) SELECT 'запись', id, $1, now(), now() + interval '1 hour' FROM c`, e.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, e.ID); !errors.Is(err, ErrHasRecords) {
		t.Errorf("Delete: %v, ожидали ErrHasRecords", err)
	}
}

func TestStorageListPagination(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()
	for _, name := range []string{"Анна", "Борис", "Вера"} {
		if _, err := s.Create(ctx, EmployeeInput{Name: name}); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		limit, offset int
		wantNames     []string
	}{
		{2, 0, []string{"Анна", "Борис"}},
		{2, 2, []string{"Вера"}},
		{2, 10, []string{}}, // за концом списка: страница пустая, но total известен
	}
	for _, tt := range tests {
		list, total, err := s.List(ctx, tt.limit, tt.offset)
		if err != nil || total != 3 || len(list) != len(tt.wantNames) {
			t.Errorf("List(%d, %d) = %+v, %d, %v", tt.limit, tt.offset, list, total, err)
			continue
		}
		for i, e := range list {
			if e.Name != tt.wantNames[i] {
				t.Errorf("List(%d, %d)[%d] = %q, ожидали %q", tt.limit, tt.offset, i, e.Name, tt.wantNames[i])
			}
		}
	}
}

func TestStorageWorkingHours(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	e, err := s.Create(ctx, EmployeeInput{Name: "Анна"})
	if err != nil {
		t.Fatal(err)
	}
	if days, err := s.WorkingHours(ctx, e.ID); err != nil || days == nil || len(days) != 0 {
		t.Errorf("новый мастер: %v, %v; ожидали пустой график", days, err)
	}

	week := []WorkingDay{{Weekday: 5, Start: "12:00", End: "21:00"}, {Weekday: 1, Start: "09:30", End: "18:00"}}
	if err := s.SetWorkingHours(ctx, e.ID, week); err != nil {
		t.Fatal(err)
	}
	days, err := s.WorkingHours(ctx, e.ID)
	want := []WorkingDay{{Weekday: 1, Start: "09:30", End: "18:00"}, {Weekday: 5, Start: "12:00", End: "21:00"}}
	if err != nil || fmt.Sprint(days) != fmt.Sprint(want) {
		t.Errorf("график по дням недели: %v, %v; ожидали %v", days, err, want)
	}

	// Замена целиком: старые дни уходят.
	if err := s.SetWorkingHours(ctx, e.ID, []WorkingDay{{Weekday: 3, Start: "10:00", End: "19:00"}}); err != nil {
		t.Fatal(err)
	}
	if days, _ := s.WorkingHours(ctx, e.ID); len(days) != 1 || days[0].Weekday != 3 {
		t.Errorf("после замены: %v", days)
	}

	// Ошибка посреди замены откатывает всю транзакцию: старый график остаётся.
	bad := []WorkingDay{{Weekday: 1, Start: "10:00", End: "19:00"}, {Weekday: 2, Start: "19:00", End: "10:00"}}
	if err := s.SetWorkingHours(ctx, e.ID, bad); err == nil {
		t.Error("БД приняла конец раньше начала, ожидали ошибку CHECK")
	}
	if days, _ := s.WorkingHours(ctx, e.ID); len(days) != 1 || days[0].Weekday != 3 {
		t.Errorf("после неудачной замены график изменился: %v", days)
	}

	for name, err := range map[string]error{
		"WorkingHours":    func() error { _, err := s.WorkingHours(ctx, missingID); return err }(),
		"SetWorkingHours": s.SetWorkingHours(ctx, missingID, nil),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s несуществующего мастера: %v, ожидали ErrNotFound", name, err)
		}
	}

	// Мастера удалили — график удаляется вместе с ним (ON DELETE CASCADE).
	if err := s.Delete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM employee_working_hours`).Scan(&left); err != nil || left != 0 {
		t.Errorf("после удаления мастера осталось %d дней графика, %v", left, err)
	}
}

func TestStorageServices(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	anna, err := s.Create(ctx, EmployeeInput{Name: "Анна"})
	if err != nil {
		t.Fatal(err)
	}
	boris, err := s.Create(ctx, EmployeeInput{Name: "Борис"})
	if err != nil {
		t.Fatal(err)
	}
	var haircut, manicure string
	if err := db.QueryRow(ctx, `INSERT INTO services (name, duration_minutes, price_kopecks) VALUES ('Стрижка', 60, 150000) RETURNING id`).Scan(&haircut); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO services (name, duration_minutes, price_kopecks) VALUES ('Маникюр', 30, 90000) RETURNING id`).Scan(&manicure); err != nil {
		t.Fatal(err)
	}

	if err := s.SetServices(ctx, anna.ID, []string{haircut, manicure}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServices(ctx, boris.ID, []string{haircut}); err != nil {
		t.Fatal(err)
	}

	list, err := s.Services(ctx, anna.ID)
	if err != nil || len(list) != 2 || list[0].Name != "Маникюр" || list[1].DurationMinutes != 60 {
		t.Errorf("услуги Анны по алфавиту: %+v, %v", list, err)
	}
	if who, err := s.ByService(ctx, haircut); err != nil || len(who) != 2 || who[0].Name != "Анна" {
		t.Errorf("кто стрижёт: %+v, %v", who, err)
	}
	if who, err := s.ByService(ctx, manicure); err != nil || len(who) != 1 {
		t.Errorf("кто делает маникюр: %+v, %v", who, err)
	}

	for _, tt := range []struct {
		employee, service string
		want              bool
	}{{anna.ID, manicure, true}, {boris.ID, manicure, false}, {boris.ID, haircut, true}} {
		if got, err := s.Provides(ctx, tt.employee, tt.service); err != nil || got != tt.want {
			t.Errorf("Provides(%s, %s) = %v, %v; ожидали %v", tt.employee, tt.service, got, err, tt.want)
		}
	}

	// Несуществующая услуга в списке — ошибка, и старый набор остаётся (транзакция).
	if err := s.SetServices(ctx, anna.ID, []string{haircut, missingID}); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("SetServices с несуществующей услугой: %v", err)
	}
	if list, _ := s.Services(ctx, anna.ID); len(list) != 2 {
		t.Errorf("после неудачной замены набор изменился: %+v", list)
	}

	// Пустой набор — мастер не оказывает ничего.
	if err := s.SetServices(ctx, boris.ID, nil); err != nil {
		t.Fatal(err)
	}
	if list, err := s.Services(ctx, boris.ID); err != nil || list == nil || len(list) != 0 {
		t.Errorf("пустой набор: %+v, %v", list, err)
	}

	for name, err := range map[string]error{
		"Services":    func() error { _, err := s.Services(ctx, missingID); return err }(),
		"SetServices": s.SetServices(ctx, missingID, nil),
		"Provides":    func() error { _, err := s.Provides(ctx, missingID, haircut); return err }(),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s несуществующего мастера: %v, ожидали ErrNotFound", name, err)
		}
	}
	if _, err := s.ByService(ctx, missingID); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("ByService несуществующей услуги: %v", err)
	}
}
