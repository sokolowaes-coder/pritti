package employees

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"pritti/internal/services"
)

// Ошибки хранилища — маркеры для errors.Is. Что ответить пользователю, решает хендлер.
var (
	ErrNotFound   = errors.New("employee not found")
	ErrHasRecords = errors.New("employee has records")
)

const employeeColumns = `id, name, created_at`

type Storage struct {
	db *pgxpool.Pool
}

func NewStorage(db *pgxpool.Pool) *Storage {
	return &Storage{db: db}
}

// List возвращает limit сотрудников, начиная с offset, и сколько сотрудников всего.
func (s *Storage) List(ctx context.Context, limit, offset int) ([]Employee, int, error) {
	// count(*) OVER() считается до LIMIT: общее число приходит тем же запросом,
	// что и страница, поэтому они не разойдутся, если таблица меняется между запросами.
	rows, err := s.db.Query(ctx,
		`SELECT `+employeeColumns+`, count(*) OVER() FROM employees ORDER BY created_at, id LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	var total int
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Employee, error) {
		var e Employee
		err := row.Scan(&e.ID, &e.Name, &e.CreatedAt, &total)
		return e, err
	})
	if err != nil {
		return nil, 0, err
	}
	if len(list) > 0 {
		return list, total, nil
	}
	// Пустая страница — строк с count нет: offset мог уйти за конец списка. Считаем отдельно.
	if offset > 0 {
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM employees`).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return []Employee{}, total, nil
}

func scanEmployee(row pgx.Row) (Employee, error) {
	var e Employee
	err := row.Scan(&e.ID, &e.Name, &e.CreatedAt)
	return e, err
}

func (s *Storage) GetByID(ctx context.Context, id string) (Employee, error) {
	e, err := scanEmployee(s.db.QueryRow(ctx, `SELECT `+employeeColumns+` FROM employees WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return e, err
}

func (s *Storage) Create(ctx context.Context, in EmployeeInput) (Employee, error) {
	return scanEmployee(s.db.QueryRow(ctx,
		`INSERT INTO employees (name) VALUES ($1) RETURNING `+employeeColumns,
		in.Name,
	))
}

func (s *Storage) Update(ctx context.Context, id string, in EmployeeInput) (Employee, error) {
	e, err := scanEmployee(s.db.QueryRow(ctx,
		`UPDATE employees SET name = $2 WHERE id = $1 RETURNING `+employeeColumns,
		id, in.Name,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return Employee{}, ErrNotFound
	}
	return e, err
}

func (s *Storage) Delete(ctx context.Context, id string) error {
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

// WorkingHours возвращает рабочие часы мастера по дням недели. ErrNotFound — мастера нет.
func (s *Storage) WorkingHours(ctx context.Context, employeeID string) ([]WorkingDay, error) {
	rows, err := s.db.Query(ctx,
		`SELECT weekday, to_char(start_time, 'HH24:MI'), to_char(end_time, 'HH24:MI')
		 FROM employee_working_hours WHERE employee_id = $1 ORDER BY weekday`,
		employeeID,
	)
	if err != nil {
		return nil, err
	}
	days, err := pgx.CollectRows(rows, pgx.RowToStructByPos[WorkingDay])
	if err != nil {
		return nil, err
	}
	if len(days) > 0 {
		return days, nil
	}
	// Пустой график — либо мастер не работает ни дня, либо мастера нет вовсе.
	if err := s.mustExist(ctx, employeeID); err != nil {
		return nil, err
	}
	return []WorkingDay{}, nil
}

// SetWorkingHours заменяет весь график мастера в одной транзакции: в БД оказывается
// либо новый график целиком, либо остаётся старый — половины не бывает.
func (s *Storage) SetWorkingHours(ctx context.Context, employeeID string, days []WorkingDay) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// FOR UPDATE блокирует строку мастера до конца транзакции: два одновременных
		// изменения графика идут по очереди, а не удаляют и вставляют дни вперемешку.
		var found bool
		err := tx.QueryRow(ctx, `SELECT true FROM employees WHERE id = $1 FOR UPDATE`, employeeID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM employee_working_hours WHERE employee_id = $1`, employeeID); err != nil {
			return err
		}
		for _, d := range days {
			if _, err := tx.Exec(ctx,
				`INSERT INTO employee_working_hours (employee_id, weekday, start_time, end_time) VALUES ($1, $2, $3::time, $4::time)`,
				employeeID, d.Weekday, d.Start, d.End,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// ErrServiceNotFound — в списке услуг мастера есть несуществующая услуга.
var ErrServiceNotFound = errors.New("service not found")

// Services возвращает услуги, которые оказывает мастер, по алфавиту. ErrNotFound — мастера нет.
func (s *Storage) Services(ctx context.Context, employeeID string) ([]services.Service, error) {
	rows, err := s.db.Query(ctx,
		`SELECT s.id, s.name, s.duration_minutes, s.price_kopecks, s.created_at
		 FROM employee_services es JOIN services s ON s.id = es.service_id
		 WHERE es.employee_id = $1 ORDER BY lower(s.name), s.id`,
		employeeID,
	)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[services.Service])
	if err != nil {
		return nil, err
	}
	if len(list) > 0 {
		return list, nil
	}
	if err := s.mustExist(ctx, employeeID); err != nil {
		return nil, err
	}
	return []services.Service{}, nil
}

// SetServices заменяет набор услуг мастера в одной транзакции — как SetWorkingHours.
func (s *Storage) SetServices(ctx context.Context, employeeID string, serviceIDs []string) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var found bool
		err := tx.QueryRow(ctx, `SELECT true FROM employees WHERE id = $1 FOR UPDATE`, employeeID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM employee_services WHERE employee_id = $1`, employeeID); err != nil {
			return err
		}
		// Все услуги одной вставкой: unnest разворачивает массив id в строки.
		_, err = tx.Exec(ctx,
			`INSERT INTO employee_services (employee_id, service_id) SELECT $1, unnest($2::uuid[])`,
			employeeID, serviceIDs,
		)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrServiceNotFound
		}
		return err
	})
}

// Provides — оказывает ли мастер услугу. ErrNotFound — мастера нет.
func (s *Storage) Provides(ctx context.Context, employeeID, serviceID string) (bool, error) {
	var exists, provides bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM employees WHERE id = $1),
		        EXISTS (SELECT 1 FROM employee_services WHERE employee_id = $1 AND service_id = $2)`,
		employeeID, serviceID,
	).Scan(&exists, &provides)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	return provides, nil
}

// ByService возвращает мастеров, которые оказывают услугу, по имени.
// ErrServiceNotFound — услуги нет.
func (s *Storage) ByService(ctx context.Context, serviceID string) ([]Employee, error) {
	rows, err := s.db.Query(ctx,
		`SELECT e.id, e.name, e.created_at
		 FROM employee_services es JOIN employees e ON e.id = es.employee_id
		 WHERE es.service_id = $1 ORDER BY e.name, e.id`,
		serviceID,
	)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Employee])
	if err != nil {
		return nil, err
	}
	if len(list) > 0 {
		return list, nil
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM services WHERE id = $1)`, serviceID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrServiceNotFound
	}
	return []Employee{}, nil
}

// mustExist возвращает ErrNotFound, если мастера нет.
func (s *Storage) mustExist(ctx context.Context, employeeID string) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM employees WHERE id = $1)`, employeeID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
