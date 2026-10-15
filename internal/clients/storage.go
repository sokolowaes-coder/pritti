package clients

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("клиент не найден")
	ErrHasRecords = errors.New("у клиента есть записи, сначала удалите или переназначьте их")
	ErrPhoneTaken = errors.New("клиент с таким телефоном уже есть")
)

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

func (s *Storage) List(ctx context.Context) ([]Client, error) {
	rows, err := s.db.Query(ctx, `SELECT id, full_name, phone, created_at FROM clients ORDER BY id`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Client])
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Client{}
	}
	return list, nil
}

func (s *Storage) Create(ctx context.Context, in ClientInput) (Client, error) {
	var c Client
	err := s.db.QueryRow(ctx,
		`INSERT INTO clients (full_name, phone) VALUES ($1, $2) RETURNING id, full_name, phone, created_at`,
		in.FullName, in.Phone,
	).Scan(&c.ID, &c.FullName, &c.Phone, &c.CreatedAt)
	return c, phoneError(err)
}

func (s *Storage) Update(ctx context.Context, id int64, in ClientInput) (Client, error) {
	var c Client
	err := s.db.QueryRow(ctx,
		`UPDATE clients SET full_name = $2, phone = $3 WHERE id = $1 RETURNING id, full_name, phone, created_at`,
		id, in.FullName, in.Phone,
	).Scan(&c.ID, &c.FullName, &c.Phone, &c.CreatedAt)
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

func (s *Storage) Delete(ctx context.Context, id int64) error {
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
