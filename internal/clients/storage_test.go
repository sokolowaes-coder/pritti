package clients

import (
	"context"
	"errors"
	"testing"

	"go-education/internal/testdb"
)

func TestStorageCRUD(t *testing.T) {
	db := testdb.New(t)
	s := NewStorage(db)
	ctx := context.Background()

	list, err := s.List(ctx)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("пустой список: %v, %v", list, err)
	}

	created, err := s.Create(ctx, ClientInput{FullName: "Иванова Анна", Phone: "+79161234567"})
	if err != nil || created.ID == 0 || created.FullName != "Иванова Анна" || created.Phone != "+79161234567" || created.CreatedAt.IsZero() {
		t.Fatalf("Create: %+v, %v", created, err)
	}

	updated, err := s.Update(ctx, created.ID, ClientInput{FullName: "Петрова Ольга", Phone: "+79161234567"})
	if err != nil || updated.ID != created.ID || updated.FullName != "Петрова Ольга" {
		t.Errorf("Update: %+v, %v", updated, err)
	}

	list, err = s.List(ctx)
	if err != nil || len(list) != 1 || list[0].FullName != "Петрова Ольга" {
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

	if _, err := s.Update(ctx, 999, ClientInput{FullName: "x", Phone: "+79161234567"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v, ожидали ErrNotFound", err)
	}
	if err := s.Delete(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v, ожидали ErrNotFound", err)
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
