package clients

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

	created, err := s.Create(ctx, ClientInput{FullName: "Иванова Анна", Phone: "+79161234567"})
	if err != nil || created.ID == "" || created.FullName != "Иванова Анна" || created.Phone != "+79161234567" || created.CreatedAt.IsZero() {
		t.Fatalf("Create: %+v, %v", created, err)
	}

	updated, err := s.Update(ctx, created.ID, ClientInput{FullName: "Петрова Ольга", Phone: "+79161234567"})
	if err != nil || updated.ID != created.ID || updated.FullName != "Петрова Ольга" {
		t.Errorf("Update: %+v, %v", updated, err)
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil || got != updated {
		t.Errorf("GetByID: %+v, %v", got, err)
	}

	list, total, err = s.List(ctx, 20, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].FullName != "Петрова Ольга" {
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

	if _, err := s.Update(ctx, missingID, ClientInput{FullName: "x", Phone: "+79161234567"}); !errors.Is(err, ErrNotFound) {
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
	if _, err := s.Create(context.Background(), ClientInput{FullName: "   ", Phone: "+79161234567"}); err == nil {
		t.Error("БД приняла пустое имя, ожидали ошибку CHECK")
	}
}

func TestStoragePhone(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()

	a, err := s.Create(ctx, ClientInput{FullName: "Анна", Phone: "+79161234567"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, ClientInput{FullName: "Борис", Phone: "+79161234567"}); !errors.Is(err, ErrPhoneTaken) {
		t.Errorf("Create с занятым телефоном: %v, ожидали ErrPhoneTaken", err)
	}
	b, err := s.Create(ctx, ClientInput{FullName: "Борис", Phone: "+79990001122"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, b.ID, ClientInput{FullName: "Борис", Phone: a.Phone}); !errors.Is(err, ErrPhoneTaken) {
		t.Errorf("Update на чужой телефон: %v, ожидали ErrPhoneTaken", err)
	}
	if _, err := s.Create(ctx, ClientInput{FullName: "Вера", Phone: "89161234567"}); err == nil {
		t.Error("БД приняла ненормализованный телефон, ожидали ошибку CHECK")
	}
}

func TestStorageDeleteWithRecords(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	c, err := s.Create(ctx, ClientInput{FullName: "Иванова Анна", Phone: "+79161234567"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		WITH e AS (INSERT INTO employees (name) VALUES ('Мария') RETURNING id)
		INSERT INTO records (name, client_id, employee_id) SELECT 'запись', $1, id FROM e`, c.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(ctx, c.ID); !errors.Is(err, ErrHasRecords) {
		t.Errorf("Delete: %v, ожидали ErrHasRecords", err)
	}
}

func TestStorageListPagination(t *testing.T) {
	s := NewStorage(testdb.New(t))
	ctx := context.Background()
	for i, name := range []string{"Анна", "Борис", "Вера"} {
		if _, err := s.Create(ctx, ClientInput{FullName: name, Phone: fmt.Sprintf("+7916000000%d", i)}); err != nil {
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
		for i, c := range list {
			if c.FullName != tt.wantNames[i] {
				t.Errorf("List(%d, %d)[%d] = %q, ожидали %q", tt.limit, tt.offset, i, c.FullName, tt.wantNames[i])
			}
		}
	}
}
