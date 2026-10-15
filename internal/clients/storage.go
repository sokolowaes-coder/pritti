package clients

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ошибки хранилища — маркеры для errors.Is. Что ответить пользователю, решает хендлер.
var (
	ErrNotFound   = errors.New("client not found")
	ErrHasRecords = errors.New("client has records")
	ErrPhoneTaken = errors.New("client phone taken")
)

const clientColumns = `id, full_name, phone, created_at`

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

// List возвращает limit клиентов, начиная с offset, и сколько клиентов всего.
func (s *Storage) List(ctx context.Context, limit, offset int) ([]Client, int, error) {
	// count(*) OVER() считается до LIMIT: общее число приходит тем же запросом,
	// что и страница, поэтому они не разойдутся, если таблица меняется между запросами.
	rows, err := s.db.Query(ctx,
		`SELECT `+clientColumns+`, count(*) OVER() FROM clients ORDER BY created_at, id LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	var total int
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Client, error) {
		var c Client
		err := row.Scan(&c.ID, &c.FullName, &c.Phone, &c.CreatedAt, &total)
		return c, err
	})
	if err != nil {
		return nil, 0, err
	}
	if len(list) > 0 {
		return list, total, nil
	}
	// Пустая страница — строк с count нет: offset мог уйти за конец списка. Считаем отдельно.
	if offset > 0 {
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM clients`).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return []Client{}, total, nil
}

func scanClient(row pgx.Row) (Client, error) {
	var c Client
	err := row.Scan(&c.ID, &c.FullName, &c.Phone, &c.CreatedAt)
	return c, err
}

func (s *Storage) GetByID(ctx context.Context, id string) (Client, error) {
	c, err := scanClient(s.db.QueryRow(ctx, `SELECT `+clientColumns+` FROM clients WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return c, err
}

func (s *Storage) Create(ctx context.Context, in ClientInput) (Client, error) {
	c, err := scanClient(s.db.QueryRow(ctx,
		`INSERT INTO clients (full_name, phone) VALUES ($1, $2) RETURNING `+clientColumns,
		in.FullName, in.Phone,
	))
	return c, phoneError(err)
}

func (s *Storage) Update(ctx context.Context, id string, in ClientInput) (Client, error) {
	c, err := scanClient(s.db.QueryRow(ctx,
		`UPDATE clients SET full_name = $2, phone = $3 WHERE id = $1 RETURNING `+clientColumns,
		id, in.FullName, in.Phone,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return c, phoneError(err)
}

// phoneError превращает нарушение уникальности телефона (код 23505) в ErrPhoneTaken.
func phoneError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "clients_phone_key" {
		return ErrPhoneTaken
	}
	return err
}

func (s *Storage) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM clients WHERE id = $1`, id)
	// 23503 — на клиента ссылаются записи, внешний ключ не даёт удалить.
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
