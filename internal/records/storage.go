package records

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound         = errors.New("запись не найдена")
	ErrClientNotFound   = errors.New("клиент не найден")
	ErrEmployeeNotFound = errors.New("сотрудник не найден")
)

const recordColumns = `id, name, description, client_id, employee_id, created_at`

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

// List возвращает страницу записей по фильтрам и общее число подходящих записей.
func (s *Storage) List(ctx context.Context, f ListFilter) ([]Record, int, error) {
	where, args := buildWhere(f)

	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM records`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(ctx,
		`SELECT `+recordColumns+` FROM records`+where+
			fmt.Sprintf(` ORDER BY id LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	records, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Record])
	if err != nil {
		return nil, 0, err
	}
	if records == nil {
		records = []Record{}
	}
	return records, total, nil
}

// buildWhere собирает WHERE из фильтров. Значения всегда идут через $1, $2...,
// а не вклеиваются в строку, — так SQL-инъекция невозможна.
func buildWhere(f ListFilter) (string, []any) {
	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if f.FullText != nil {
		fields := f.FullTextFields
		if len(fields) == 0 {
			fields = []string{"name", "description"}
		}
		p := arg("%" + escapeLike(*f.FullText) + "%")
		var ors []string
		for _, field := range fields {
			ors = append(ors, fullTextColumns[field]+" ILIKE "+p)
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	if len(f.IDs) > 0 {
		conds = append(conds, "id = ANY("+arg(f.IDs)+")")
	}
	if f.DateStart != nil {
		conds = append(conds, "created_at >= "+arg(*f.DateStart))
	}
	if f.DateEnd != nil {
		conds = append(conds, "created_at <= "+arg(*f.DateEnd))
	}

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// escapeLike экранирует % и _, чтобы они искались как обычные символы.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func scanRecord(row pgx.Row) (Record, error) {
	var rec Record
	err := row.Scan(&rec.ID, &rec.Name, &rec.Description, &rec.ClientID, &rec.EmployeeID, &rec.CreatedAt)
	return rec, err
}

func (s *Storage) GetByID(ctx context.Context, id int64) (Record, error) {
	rec, err := scanRecord(s.db.QueryRow(ctx,
		`SELECT `+recordColumns+` FROM records WHERE id = $1`,
		id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return rec, err
}

func (s *Storage) Create(ctx context.Context, in RecordInput) (Record, error) {
	rec, err := scanRecord(s.db.QueryRow(ctx,
		`INSERT INTO records (name, description, client_id, employee_id) VALUES ($1, $2, $3, $4)
		 RETURNING `+recordColumns,
		in.Name, in.Description, in.ClientID, in.EmployeeID,
	))
	return rec, foreignKeyError(err)
}

func (s *Storage) Update(ctx context.Context, id int64, in RecordInput) (Record, error) {
	rec, err := scanRecord(s.db.QueryRow(ctx,
		`UPDATE records SET name = $2, description = $3, client_id = $4, employee_id = $5 WHERE id = $1
		 RETURNING `+recordColumns,
		id, in.Name, in.Description, in.ClientID, in.EmployeeID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return rec, foreignKeyError(err)
}

// foreignKeyError превращает нарушение внешнего ключа (код 23503)
// в понятную ошибку: какого именно клиента или сотрудника нет.
func foreignKeyError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		switch pgErr.ConstraintName {
		case "records_client_id_fkey":
			return ErrClientNotFound
		case "records_employee_id_fkey":
			return ErrEmployeeNotFound
		}
	}
	return err
}

func (s *Storage) Delete(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM records WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
