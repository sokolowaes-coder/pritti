package employees

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("сотрудник не найден")
	ErrHasRecords = errors.New("у сотрудника есть записи, сначала удалите или переназначьте их")
)

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

func (s *Storage) List(ctx context.Context) ([]Employee, error) {
	rows, err := s.db.Query(ctx, `SELECT id, name, created_at FROM employees ORDER BY id`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Employee])
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Employee{}
	}
	return list, nil
}

func (s *Storage) Create(ctx context.Context, in EmployeeInput) (Employee, error) {
	var e Employee
	err := s.db.QueryRow(ctx,
		`INSERT INTO employees (name) VALUES ($1) RETURNING id, name, created_at`,
		in.Name,
	).Scan(&e.ID, &e.Name, &e.CreatedAt)
	return e, err
}

func (s *Storage) Update(ctx context.Context, id int64, in EmployeeInput) (Employee, error) {
	var e Employee
	err := s.db.QueryRow(ctx,
		`UPDATE employees SET name = $2 WHERE id = $1 RETURNING id, name, created_at`,
		id, in.Name,
	).Scan(&e.ID, &e.Name, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return e, err
}

func (s *Storage) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM employees WHERE id = $1`, id)
	// 23503 — на сотрудника ссылаются записи, внешний ключ не даёт удалить.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrHasRecords
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
