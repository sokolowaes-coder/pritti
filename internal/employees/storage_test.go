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
