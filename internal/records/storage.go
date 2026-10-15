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

// Ошибки хранилища — маркеры для errors.Is. Что ответить пользователю, решает хендлер.
var (
	ErrNotFound         = errors.New("record not found")
	ErrClientNotFound   = errors.New("record client not found")
	ErrEmployeeNotFound = errors.New("record employee not found")
	ErrEmployeeBusy     = errors.New("employee has another record at this time")
)

const recordColumns = `id, name, description, client_id, employee_id, starts_at, ends_at, status, created_at`

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

// List возвращает страницу записей по фильтрам и общее число подходящих записей.
func (s *Storage) List(ctx context.Context, f ListFilter) ([]Record, int, error) {
	where, args := buildWhere(f)
	countArgs := args

	// count(*) OVER() считается до LIMIT: общее число приходит тем же запросом,
	// что и страница, поэтому они не разойдутся, если таблица меняется между запросами.
	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Query(ctx,
		`SELECT `+recordColumns+`, count(*) OVER() FROM records`+where+
			fmt.Sprintf(` ORDER BY starts_at, id LIMIT $%d OFFSET $%d`, len(args)-1, len(args)),
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	var total int
	records, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Record, error) {
		return scanRecord(row, &total)
	})
	if err != nil {
		return nil, 0, err
	}
	if len(records) > 0 {
		return records, total, nil
	}
	// Пустая страница — строк с count нет: offset мог уйти за конец списка. Считаем отдельно.
	if f.Offset > 0 {
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM records`+where, countArgs...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return []Record{}, total, nil
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
		conds = append(conds, "id = ANY("+arg(f.IDs)+"::uuid[])")
	}
	if len(f.EmployeeIDs) > 0 {
		conds = append(conds, "employee_id = ANY("+arg(f.EmployeeIDs)+"::uuid[])")
	}
	if len(f.ClientIDs) > 0 {
		conds = append(conds, "client_id = ANY("+arg(f.ClientIDs)+"::uuid[])")
	}
	if len(f.Statuses) > 0 {
		conds = append(conds, "status = ANY("+arg(f.Statuses)+"::text[])")
	}
	if f.DateStart != nil {
		conds = append(conds, "starts_at >= "+arg(*f.DateStart))
	}
	if f.DateEnd != nil {
		conds = append(conds, "starts_at <= "+arg(*f.DateEnd))
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

// scanRecord читает колонки recordColumns и, если передан extra, — ещё одну колонку после них.
func scanRecord(row pgx.Row, extra ...any) (Record, error) {
	var rec Record
	dest := append([]any{&rec.ID, &rec.Name, &rec.Description, &rec.ClientID, &rec.EmployeeID,
		&rec.StartsAt, &rec.EndsAt, &rec.Status, &rec.CreatedAt}, extra...)
	err := row.Scan(dest...)
	return rec, err
}

func (s *Storage) GetByID(ctx context.Context, id string) (Record, error) {
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
		`INSERT INTO records (name, description, client_id, employee_id, starts_at, ends_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+recordColumns,
		in.Name, in.Description, in.ClientID, in.EmployeeID, in.StartsAt, in.EndsAt, in.Status,
	))
	return rec, constraintError(err)
}

func (s *Storage) Update(ctx context.Context, id string, in RecordInput) (Record, error) {
	rec, err := scanRecord(s.db.QueryRow(ctx,
		`UPDATE records SET name = $2, description = $3, client_id = $4, employee_id = $5,
		                    starts_at = $6, ends_at = $7, status = $8
		 WHERE id = $1
		 RETURNING `+recordColumns,
		id, in.Name, in.Description, in.ClientID, in.EmployeeID, in.StartsAt, in.EndsAt, in.Status,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return rec, constraintError(err)
}

// constraintError превращает нарушения ограничений БД в понятные ошибки:
// 23503 — нет такого клиента или сотрудника, 23P01 — у мастера уже есть запись на это время.
func constraintError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23503" && pgErr.ConstraintName == "records_client_id_fkey":
		return ErrClientNotFound
	case pgErr.Code == "23503" && pgErr.ConstraintName == "records_employee_id_fkey":
		return ErrEmployeeNotFound
	case pgErr.Code == "23P01" && pgErr.ConstraintName == "records_employee_no_overlap":
		return ErrEmployeeBusy
	}
	return err
}

func (s *Storage) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM records WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
