// Package services — услуги салона: название, длительность и цена.
package services

import "time"

type Service struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	DurationMinutes int       `json:"duration_minutes"`
	PriceKopecks    int64     `json:"price_kopecks"` // в копейках: 150000 — 1500 ₽
	CreatedAt       time.Time `json:"created_at"`
}

// Duration — длительность услуги для расчёта конца записи.
func (s Service) Duration() time.Duration {
	return time.Duration(s.DurationMinutes) * time.Minute
}

// ServiceInput — поля, которые передают при создании и изменении услуги.
type ServiceInput struct {
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
	PriceKopecks    int64  `json:"price_kopecks"`
}
