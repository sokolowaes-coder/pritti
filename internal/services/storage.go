package services

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ошибки хранилища — маркеры для errors.Is. Что ответить пользователю, решает хендлер.
var (
	ErrNotFound   = errors.New("service not found")
	ErrHasRecords = errors.New("service has records")
	ErrNameTaken  = errors.New("service name taken")
)

const serviceColumns = `id, name, duration_minutes, price_kopecks, created_at`

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

// List возвращает limit услуг по алфавиту, начиная с offset, и сколько услуг всего.
func (s *Storage) List(ctx context.Context, limit, offset int) ([]Service, int, error) {
	// count(*) OVER() считается до LIMIT: общее число приходит тем же запросом, что и страница.
	rows, err := s.db.Query(ctx,
		`SELECT `+serviceColumns+`, count(*) OVER() FROM services ORDER BY lower(name), id LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	var total int
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Service, error) {
		return scanService(row, &total)
	})
	if err != nil {
		return nil, 0, err
	}
	if len(list) > 0 {
		return list, total, nil
	}
	// Пустая страница — строк с count нет: offset мог уйти за конец списка. Считаем отдельно.
	if offset > 0 {
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM services`).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return []Service{}, total, nil
}

// scanService читает колонки serviceColumns и, если передан extra, — ещё одну после них.
func scanService(row pgx.Row, extra ...any) (Service, error) {
	var sv Service
	dest := append([]any{&sv.ID, &sv.Name, &sv.DurationMinutes, &sv.PriceKopecks, &sv.CreatedAt}, extra...)
	err := row.Scan(dest...)
	return sv, err
}

func (s *Storage) GetByID(ctx context.Context, id string) (Service, error) {
	sv, err := scanService(s.db.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return sv, err
}

func (s *Storage) Create(ctx context.Context, in ServiceInput) (Service, error) {
	sv, err := scanService(s.db.QueryRow(ctx,
		`INSERT INTO services (name, duration_minutes, price_kopecks) VALUES ($1, $2, $3) RETURNING `+serviceColumns,
		in.Name, in.DurationMinutes, in.PriceKopecks,
	))
	return sv, nameError(err)
}

func (s *Storage) Update(ctx context.Context, id string, in ServiceInput) (Service, error) {
	sv, err := scanService(s.db.QueryRow(ctx,
		`UPDATE services SET name = $2, duration_minutes = $3, price_kopecks = $4 WHERE id = $1 RETURNING `+serviceColumns,
		id, in.Name, in.DurationMinutes, in.PriceKopecks,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return sv, nameError(err)
}

// nameError превращает нарушение уникальности названия (код 23505) в ErrNameTaken.
func nameError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "services_name_key" {
		return ErrNameTaken
	}
	return err
}

func (s *Storage) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM services WHERE id = $1`, id)
	// 23503 — на услугу ссылаются записи, внешний ключ не даёт удалить.
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
