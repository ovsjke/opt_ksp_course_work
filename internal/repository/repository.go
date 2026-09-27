package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"pavel_ovsyannikov/internal/domain"
)

func dbError(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if e != nil {
		return fmt.Errorf("database: %w", e)
	}
	return nil
}
func (r *Repository) User(ctx context.Context, login string) (domain.User, error) {
	var u domain.User
	e := r.Pool.QueryRow(ctx, `SELECT id,login,password_hash FROM users WHERE login=$1`, login).Scan(&u.ID, &u.Login, &u.PasswordHash)
	return u, dbError(e)
}
func (r *Repository) CreateSession(ctx context.Context, id string, uid int64, expires time.Time) error {
	_, e := r.Pool.Exec(ctx, `INSERT INTO sessions VALUES($1,$2,now(),$3)`, id, uid, expires)
	return dbError(e)
}
func (r *Repository) Session(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	e := r.Pool.QueryRow(ctx, `SELECT u.id,u.login FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=$1 AND s.expires_at>now()`, id).Scan(&u.ID, &u.Login)
	return u, dbError(e)
}
func (r *Repository) DeleteSession(ctx context.Context, id string) error {
	_, e := r.Pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return dbError(e)
}
func (r *Repository) Services(ctx context.Context) ([]domain.Service, error) {
	rows, e := r.Pool.Query(ctx, `SELECT id,name,duration_minutes FROM services ORDER BY id`)
	if e != nil {
		return nil, dbError(e)
	}
	defer rows.Close()
	out := []domain.Service{}
	for rows.Next() {
		var v domain.Service
		if e = rows.Scan(&v.ID, &v.Name, &v.DurationMinutes); e != nil {
			return nil, dbError(e)
		}
		out = append(out, v)
	}
	return out, dbError(rows.Err())
}
func (r *Repository) Specialists(ctx context.Context) ([]domain.Specialist, error) {
	rows, e := r.Pool.Query(ctx, `SELECT id,name FROM specialists ORDER BY id`)
	if e != nil {
		return nil, dbError(e)
	}
	defer rows.Close()
	out := []domain.Specialist{}
	for rows.Next() {
		var v domain.Specialist
		if e = rows.Scan(&v.ID, &v.Name); e != nil {
			return nil, dbError(e)
		}
		out = append(out, v)
	}
	return out, dbError(rows.Err())
}

const columns = `a.id,a.user_id,a.specialist_id,a.service_id,a.slot_id,a.status,a.created_at,a.cancelled_at,sp.id,sp.name,sv.id,sv.name,sv.duration_minutes,s.id,s.specialist_id,s.starts_at,s.ends_at`
const joins = ` FROM appointments a JOIN specialists sp ON sp.id=a.specialist_id JOIN services sv ON sv.id=a.service_id JOIN slots s ON s.id=a.slot_id `

func scanAppointment(row interface{ Scan(...any) error }) (domain.Appointment, error) {
	var a domain.Appointment
	e := row.Scan(&a.ID, &a.UserID, &a.SpecialistID, &a.ServiceID, &a.SlotID, &a.Status, &a.CreatedAt, &a.CancelledAt, &a.Specialist.ID, &a.Specialist.Name, &a.Service.ID, &a.Service.Name, &a.Service.DurationMinutes, &a.Slot.ID, &a.Slot.SpecialistID, &a.Slot.StartsAt, &a.Slot.EndsAt)
	return a, dbError(e)
}
func (r *Repository) Appointment(ctx context.Context, uid, id int64) (domain.Appointment, error) {
	return scanAppointment(r.Pool.QueryRow(ctx, `SELECT `+columns+joins+` WHERE a.id=$1 AND a.user_id=$2`, id, uid))
}
func (r *Repository) Appointments(ctx context.Context, uid int64, f domain.Filter) (domain.Page, error) {
	out := domain.Page{Items: []domain.Appointment{}, Page: f.Page, Size: f.Size}
	where := ` WHERE a.user_id=$1 AND ($2::bigint=0 OR a.specialist_id=$2) AND ($3::bigint=0 OR a.service_id=$3) AND ($4::text='' OR a.status=$4) AND s.starts_at >= $5 AND s.starts_at < $6`
	args := []any{uid, f.SpecialistID, f.ServiceID, f.Status, f.From, f.To}
	e := r.Pool.QueryRow(ctx, `SELECT count(*)`+joins+where, args...).Scan(&out.Total)
	if e != nil {
		return out, dbError(e)
	}
	args = append(args, f.Size, (f.Page-1)*f.Size)
	rows, e := r.Pool.Query(ctx, `SELECT `+columns+joins+where+` ORDER BY s.starts_at,a.id LIMIT $7 OFFSET $8`, args...)
	if e != nil {
		return out, dbError(e)
	}
	defer rows.Close()
	for rows.Next() {
		a, e := scanAppointment(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, a)
	}
	return out, dbError(rows.Err())
}
func (r *Repository) Slots(ctx context.Context, service int64, from, to time.Time) ([]domain.Slot, error) {
	var exists bool
	if e := r.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM services WHERE id=$1)`, service).Scan(&exists); e != nil {
		return nil, dbError(e)
	}
	if !exists {
		return nil, domain.ErrNotFound
	}
	rows, e := r.Pool.Query(ctx, `
SELECT s.id, s.specialist_id, s.starts_at, s.ends_at
FROM slots s
JOIN specialist_services ss ON ss.specialist_id = s.specialist_id
JOIN services sv ON sv.id = ss.service_id
WHERE sv.id = $1 AND s.starts_at >= $2 AND s.starts_at < $3
  AND s.ends_at - s.starts_at >= make_interval(mins => sv.duration_minutes)
  AND NOT EXISTS (
      SELECT 1 FROM appointments a JOIN slots occupied ON occupied.id = a.slot_id
      WHERE a.status = 'booked' AND a.specialist_id = s.specialist_id
        AND occupied.starts_at < s.ends_at AND occupied.ends_at > s.starts_at
  )
ORDER BY s.starts_at, s.id LIMIT 200`, service, from, to)
	if e != nil {
		return nil, dbError(e)
	}
	defer rows.Close()
	out := []domain.Slot{}
	for rows.Next() {
		var s domain.Slot
		if e = rows.Scan(&s.ID, &s.SpecialistID, &s.StartsAt, &s.EndsAt); e != nil {
			return nil, dbError(e)
		}
		out = append(out, s)
	}
	return out, dbError(rows.Err())
}
func (r *Repository) Book(ctx context.Context, uid, slotID, serviceID int64) (int64, error) {
	tx, e := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return 0, dbError(e)
	}
	defer tx.Rollback(ctx)
	var s domain.Slot
	e = tx.QueryRow(ctx, `SELECT id,specialist_id,starts_at,ends_at FROM slots WHERE id=$1`, slotID).Scan(&s.ID, &s.SpecialistID, &s.StartsAt, &s.EndsAt)
	if e != nil {
		return 0, dbError(e)
	}
	// Every booking for this specialist takes the same row lock. READ COMMITTED
	// gives the following overlap query a fresh snapshot after a lock wait.
	var locked int64
	if e = tx.QueryRow(ctx, `SELECT id FROM specialists WHERE id=$1 FOR UPDATE`, s.SpecialistID).Scan(&locked); e != nil {
		return 0, dbError(e)
	}
	var v domain.Service
	if e = tx.QueryRow(ctx, `SELECT id,name,duration_minutes FROM services WHERE id=$1`, serviceID).Scan(&v.ID, &v.Name, &v.DurationMinutes); e != nil {
		return 0, dbError(e)
	}
	var supported, busy bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM specialist_services WHERE specialist_id=$1 AND service_id=$2),EXISTS(SELECT 1 FROM appointments a JOIN slots s ON s.id=a.slot_id WHERE a.specialist_id=$1 AND a.status='booked' AND s.starts_at<$4 AND s.ends_at>$3)`, s.SpecialistID, serviceID, s.StartsAt, s.EndsAt).Scan(&supported, &busy); e != nil {
		return 0, dbError(e)
	}
	if e = domain.ValidateBooking(s, v, supported, busy); e != nil {
		return 0, e
	}
	var id int64
	if e = tx.QueryRow(ctx, `INSERT INTO appointments(user_id,specialist_id,service_id,slot_id,status) VALUES($1,$2,$3,$4,'booked') RETURNING id`, uid, s.SpecialistID, serviceID, slotID).Scan(&id); e != nil {
		return 0, dbError(e)
	}
	return id, dbError(tx.Commit(ctx))
}
func (r *Repository) Cancel(ctx context.Context, uid, id int64) error {
	tag, e := r.Pool.Exec(ctx, `UPDATE appointments SET status='cancelled',cancelled_at=COALESCE(cancelled_at,now()) WHERE id=$1 AND user_id=$2`, id, uid)
	if e != nil {
		return dbError(e)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
func (r *Repository) Dashboard(ctx context.Context, from, to time.Time) (domain.Dashboard, error) {
	d := domain.Dashboard{Specialists: []domain.Load{}}
	e := r.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE a.status='booked'),count(*) FILTER(WHERE a.status='cancelled') FROM appointments a JOIN slots s ON s.id=a.slot_id WHERE s.starts_at >= $1 AND s.starts_at < $2`, from, to).Scan(&d.Total, &d.Booked, &d.Cancelled)
	if e != nil {
		return d, dbError(e)
	}
	d.CancellationRate = domain.Percent(d.Cancelled, d.Total)
	rows, e := r.Pool.Query(ctx, `
SELECT sp.id, sp.name,
    COALESCE((
        SELECT sum(extract(epoch FROM (s.ends_at - s.starts_at)) / 60)
        FROM appointments a JOIN slots s ON s.id = a.slot_id
        WHERE a.specialist_id = sp.id AND a.status = 'booked'
          AND s.starts_at >= $1 AND s.starts_at < $2
    ), 0)::float8,
    COALESCE((
        SELECT sum(extract(epoch FROM (s.ends_at - s.starts_at)) / 60)
        FROM slots s WHERE s.specialist_id = sp.id
          AND s.starts_at >= $1 AND s.starts_at < $2
    ), 0)::float8
FROM specialists sp ORDER BY sp.id`, from, to)
	if e != nil {
		return d, dbError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.Load
		if e = rows.Scan(&l.SpecialistID, &l.Name, &l.BookedMinutes, &l.AvailableMinutes); e != nil {
			return d, dbError(e)
		}
		if l.AvailableMinutes > 0 {
			l.Utilization = 100 * l.BookedMinutes / l.AvailableMinutes
		}
		d.Specialists = append(d.Specialists, l)
	}
	return d, dbError(rows.Err())
}
