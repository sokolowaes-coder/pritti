package services

import (
	"context"
	"errors"
	"testing"

	"pritti/internal/testdb"
)

// missingID — валидный UUID, которого нет в базе.
const missingID = "00000000-0000-0000-0000-000000000999"

func TestStorageCRUD(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()

	created, err := s.Create(ctx, ServiceInput{Name: "Стрижка", DurationMinutes: 60, PriceKopecks: 150000})
	if err != nil || created.ID == "" || created.Name != "Стрижка" || created.DurationMinutes != 60 || created.PriceKopecks != 150000 {
		t.Fatalf("Create: %+v, %v", created, err)
	}
	updated, err := s.Update(ctx, created.ID, ServiceInput{Name: "Стрижка мужская", DurationMinutes: 45, PriceKopecks: 120000})
	if err != nil || updated.Name != "Стрижка мужская" || updated.DurationMinutes != 45 {
		t.Errorf("Update: %+v, %v", updated, err)
	}
	got, err := s.GetByID(ctx, created.ID)
	if err != nil || got != updated {
		t.Errorf("GetByID: %+v, %v", got, err)
	}
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
	for name, err := range map[string]error{
		"GetByID": func() error { _, err := s.GetByID(ctx, missingID); return err }(),
		"Update": func() error {
			_, err := s.Update(ctx, missingID, ServiceInput{Name: "x", DurationMinutes: 5})
			return err
		}(),
		"Delete": s.Delete(ctx, missingID),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s несуществующей: %v, ожидали ErrNotFound", name, err)
		}
	}
}

func TestStorageListSortedByName(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()
	for _, n := range []string{"маникюр", "Окрашивание", "Брови"} {
		if _, err := s.Create(ctx, ServiceInput{Name: n, DurationMinutes: 30}); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := s.List(ctx, 2, 0)
	if err != nil || total != 3 || len(list) != 2 || list[0].Name != "Брови" || list[1].Name != "маникюр" {
		t.Errorf("List: %+v, %d, %v", list, total, err)
	}
	if list, total, _ = s.List(ctx, 2, 10); len(list) != 0 || total != 3 {
		t.Errorf("за концом списка: %+v, %d", list, total)
	}
}

func TestStorageConstraints(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	sv, err := s.Create(ctx, ServiceInput{Name: "Стрижка", DurationMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, ServiceInput{Name: "СТРИЖКА", DurationMinutes: 30}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("то же название в другом регистре: %v, ожидали ErrNameTaken", err)
	}
	for name, in := range map[string]ServiceInput{
		"пустое название":    {Name: " ", DurationMinutes: 30},
		"длительность 0":     {Name: "a", DurationMinutes: 0},
		"отрицательная цена": {Name: "b", DurationMinutes: 30, PriceKopecks: -1},
	} {
		if _, err := s.Create(ctx, in); err == nil {
			t.Errorf("%s: БД приняла, ожидали ошибку CHECK", name)
		}
	}

	if _, err := db.Exec(ctx, `
		WITH c AS (INSERT INTO clients (full_name, phone) VALUES ('к', '+79161234567') RETURNING id),
		     e AS (INSERT INTO employees (name) VALUES ('м') RETURNING id)
		INSERT INTO records (name, client_id, employee_id, service_id, starts_at, ends_at)
		SELECT 'запись', c.id, e.id, $1, now(), now() + interval '1 hour' FROM c, e`, sv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, sv.ID); !errors.Is(err, ErrHasRecords) {
		t.Errorf("Delete с записями: %v, ожидали ErrHasRecords", err)
	}
}
