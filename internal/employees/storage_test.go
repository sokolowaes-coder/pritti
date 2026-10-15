package employees

import (
	"context"
	"errors"
	"testing"

	"pritti/internal/testdb"
)

// missingID — валидный UUID, которого нет в базе.
const missingID = "00000000-0000-0000-0000-000000000999"

func TestStorageCRUD(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	list, err := s.List(ctx)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("пустой список: %v, %v", list, err)
	}

	created, err := s.Create(ctx, EmployeeInput{Name: "Анна"})
	if err != nil || created.ID == "" || created.Name != "Анна" || created.CreatedAt.IsZero() {
		t.Fatalf("Create: %+v, %v", created, err)
	}

	updated, err := s.Update(ctx, created.ID, EmployeeInput{Name: "Ольга"})
	if err != nil || updated.ID != created.ID || updated.Name != "Ольга" {
		t.Errorf("Update: %+v, %v", updated, err)
	}

	list, err = s.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Ольга" {
		t.Errorf("List: %+v, %v", list, err)
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if list, _ = s.List(ctx); len(list) != 0 {
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
		INSERT INTO records (name, client_id, employee_id) SELECT 'запись', id, $1 FROM c`, e.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, e.ID); !errors.Is(err, ErrHasRecords) {
		t.Errorf("Delete: %v, ожидали ErrHasRecords", err)
	}
}
