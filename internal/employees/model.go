package employees

import "time"

type Employee struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// EmployeeInput — поля, которые передают при создании и изменении сотрудника.
type EmployeeInput struct {
	Name string `json:"name"`
}

// WorkingDay — рабочие часы мастера в один день недели, по местному времени салона (APP_TZ).
type WorkingDay struct {
	Weekday int    `json:"weekday"` // 1 — понедельник … 7 — воскресенье (ISO 8601)
	Start   string `json:"start"`   // "10:00"
	End     string `json:"end"`     // "19:00"
}

// Schedule — недельный график мастера. Дней, которых нет в Days, мастер не работает.
type Schedule struct {
	EmployeeID string       `json:"employee_id"`
	Days       []WorkingDay `json:"days"`
}
