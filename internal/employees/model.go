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
