package domain

import (
	"errors"
	"testing"
	"time"
)

func TestBookingRules(t *testing.T) {
	start := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	slot := Slot{ID: 1, SpecialistID: 1, StartsAt: start, EndsAt: start.Add(time.Hour)}
	sv := Service{ID: 1, DurationMinutes: 30}
	for _, tc := range []struct {
		name            string
		slot            Slot
		service         Service
		supported, busy bool
		want            error
	}{{"free", slot, sv, true, false, nil}, {"occupied", slot, sv, true, true, ErrConflict}, {"short", slot, Service{ID: 2, DurationMinutes: 90}, true, false, ErrInvalid}, {"unsupported", slot, sv, false, false, ErrInvalid}, {"missing", Slot{}, sv, true, false, ErrNotFound}} {
		t.Run(tc.name, func(t *testing.T) {
			if e := ValidateBooking(tc.slot, tc.service, tc.supported, tc.busy); !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
		})
	}
}
func TestOverlap(t *testing.T) {
	s := Slot{SpecialistID: 1, StartsAt: time.Unix(0, 0), EndsAt: time.Unix(3600, 0)}
	for _, tc := range []struct {
		name       string
		start, end int64
		sp         int64
		want       bool
	}{{"overlap", 1800, 5400, 1, true}, {"adjacent", 3600, 7200, 1, false}, {"other specialist", 0, 3600, 2, false}, {"enclosed", 100, 200, 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Overlaps(s, Slot{SpecialistID: tc.sp, StartsAt: time.Unix(tc.start, 0), EndsAt: time.Unix(tc.end, 0)}); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}
func TestCancellationRate(t *testing.T) {
	if Percent(1, 4) != 25 || Percent(0, 0) != 0 || Percent(4, 4) != 100 {
		t.Fatal("incorrect rate")
	}
}
