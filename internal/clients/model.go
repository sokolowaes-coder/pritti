package clients

import "time"

type Client struct {
	ID        string    `json:"id"`
	FullName  string    `json:"full_name"`
	Phone     string    `json:"phone"`
	CreatedAt time.Time `json:"created_at"`
}

// ClientInput — поля, которые передают при создании и изменении клиента.
type ClientInput struct {
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
}
