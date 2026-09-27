package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("slot overlaps an active appointment")
	ErrInvalid      = errors.New("invalid input")
	ErrUnauthorized = errors.New("invalid credentials or session")
)

type User struct {
	ID           int64  `json:"id"`
	Login        string `json:"login"`
	PasswordHash string `json:"-"`
}
type Specialist struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type Service struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	DurationMinutes int    `json:"duration_minutes"`
}
type Slot struct {
	ID           int64     `json:"id"`
	SpecialistID int64     `json:"specialist_id"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
}
type Appointment struct {
	ID           int64      `json:"id"`
	UserID       int64      `json:"user_id"`
	SpecialistID int64      `json:"specialist_id"`
	ServiceID    int64      `json:"service_id"`
	SlotID       int64      `json:"slot_id"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	CancelledAt  *time.Time `json:"cancelled_at"`
	Specialist   Specialist `json:"specialist"`
	Service      Service    `json:"service"`
	Slot         Slot       `json:"slot"`
}
type Filter struct {
	Page, Size              int
	SpecialistID, ServiceID int64
	Status                  string
	From, To                time.Time
}
type Page struct {
	Items []Appointment `json:"items"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Size  int           `json:"size"`
}
type Load struct {
	SpecialistID     int64   `json:"specialist_id"`
	Name             string  `json:"name"`
	BookedMinutes    float64 `json:"booked_minutes"`
	AvailableMinutes float64 `json:"scheduled_minutes"`
	Utilization      float64 `json:"utilization_percent"`
}
type Dashboard struct {
	Total            int     `json:"total"`
	Booked           int     `json:"booked"`
	Cancelled        int     `json:"cancelled"`
	CancellationRate float64 `json:"cancellation_rate"`
	Specialists      []Load  `json:"specialists"`
}

func Percent(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}
func ValidateBooking(s Slot, v Service, supported, busy bool) error {
	if s.ID <= 0 || v.ID <= 0 {
		return ErrNotFound
	}
	if !supported || s.EndsAt.Sub(s.StartsAt) < time.Duration(v.DurationMinutes)*time.Minute {
		return ErrInvalid
	}
	if busy {
		return ErrConflict
	}
	return nil
}
func Overlaps(a, b Slot) bool {
	return a.SpecialistID == b.SpecialistID && a.StartsAt.Before(b.EndsAt) && b.StartsAt.Before(a.EndsAt)
}
