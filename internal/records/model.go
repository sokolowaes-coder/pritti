package records

import "time"

// Статусы записи. Они же — в CHECK-ограничении таблицы (миграция 000010).
const (
	StatusScheduled = "scheduled" // запланирована
	StatusCompleted = "completed" // клиент пришёл
	StatusCancelled = "cancelled" // отменена: время мастера снова свободно
	StatusNoShow    = "no_show"   // клиент не пришёл
)

var statuses = []string{StatusScheduled, StatusCompleted, StatusCancelled, StatusNoShow}

// Record — визит клиента к мастеру с StartsAt до EndsAt.
// CreatedAt — когда запись внесли в систему, к времени визита отношения не имеет.
type Record struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ClientID    string    `json:"client_id"`
	EmployeeID  string    `json:"employee_id"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// RecordInput — уже проверенные поля записи для создания и изменения.
type RecordInput struct {
	Name        string
	Description string
	ClientID    string
	EmployeeID  string
	StartsAt    time.Time
	EndsAt      time.Time
	Status      string
}
