package records

import "time"

// ClientID и EmployeeID — указатели: у записей, созданных до появления
// клиентов и сотрудников, их нет (в JSON — null).
type Record struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ClientID    *string   `json:"client_id"`
	EmployeeID  *string   `json:"employee_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// RecordInput — поля, которые клиент передаёт при создании и изменении записи.
type RecordInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ClientID    string `json:"client_id"`
	EmployeeID  string `json:"employee_id"`
}
