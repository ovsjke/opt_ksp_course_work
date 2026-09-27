package service

import (
	"context"
	"time"

	"pavel_ovsyannikov/internal/domain"
)

// Store is the boundary used by unit and HTTP tests; production uses PostgreSQL.
type Store interface {
	Services(context.Context) ([]domain.Service, error)
	Specialists(context.Context) ([]domain.Specialist, error)
	Appointments(context.Context, int64, domain.Filter) (domain.Page, error)
	Appointment(context.Context, int64, int64) (domain.Appointment, error)
	Slots(context.Context, int64, time.Time, time.Time) ([]domain.Slot, error)
	Book(context.Context, int64, int64, int64) (int64, error)
	Cancel(context.Context, int64, int64) error
	Dashboard(context.Context, time.Time, time.Time) (domain.Dashboard, error)
}
type Service struct{ Store Store }

func (s *Service) Book(ctx context.Context, uid, slot, service int64) (domain.Appointment, error) {
	if uid <= 0 || slot <= 0 || service <= 0 {
		return domain.Appointment{}, domain.ErrInvalid
	}
	id, e := s.Store.Book(ctx, uid, slot, service)
	if e != nil {
		return domain.Appointment{}, e
	}
	return s.Store.Appointment(ctx, uid, id)
}
func (s *Service) Cancel(ctx context.Context, uid, id int64) error {
	if id <= 0 {
		return domain.ErrInvalid
	}
	return s.Store.Cancel(ctx, uid, id)
}
