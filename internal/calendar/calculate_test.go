package calendar

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeChecker struct {
	holidays  map[string]bool
	uncovered map[string]bool
}

func (f fakeChecker) Check(_ context.Context, day time.Time, _, _ string) (bool, string, []string, error) {
	s := day.Format("2006-01-02")
	if f.uncovered[s] {
		return false, "", nil, ErrCoverage
	}
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false, "weekend", []string{"v1"}, nil
	}
	if f.holidays[s] {
		return false, "holiday", []string{"v1"}, nil
	}
	return true, "business day", []string{"v1"}, nil
}
func TestCalculateDJENAndHoliday(t *testing.T) {
	r, err := Calculate(context.Background(), fakeChecker{holidays: map[string]bool{"2026-09-07": true}}, "2026-09-04", "TJMS", "NOVA ANDRADINA", 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.PublicationOn != "2026-09-08" || r.FirstCountedOn != "2026-09-09" || r.DueOn != "2026-09-11" {
		t.Fatalf("unexpected dates: %+v", r)
	}
	if len(r.VersionIDs) != 1 {
		t.Fatalf("versions: %+v", r.VersionIDs)
	}
}
func TestIncompleteCalendarYieldsNoDate(t *testing.T) {
	_, err := Calculate(context.Background(), fakeChecker{uncovered: map[string]bool{"2026-09-10": true}}, "2026-09-08", "TJMS", "NOVA ANDRADINA", 3)
	if !errors.Is(err, ErrCoverage) {
		t.Fatalf("expected coverage error, got %v", err)
	}
}
func TestYearTurnAndSuspension(t *testing.T) {
	closed := map[string]bool{}
	for d := time.Date(2026, 12, 20, 0, 0, 0, 0, time.UTC); !d.After(time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		closed[d.Format("2006-01-02")] = true
	}
	r, err := Calculate(context.Background(), fakeChecker{holidays: closed}, "2026-12-17", "TJMS", "NOVA ANDRADINA", 2)
	if err != nil {
		t.Fatal(err)
	}
	if r.PublicationOn != "2026-12-18" || r.FirstCountedOn != "2027-01-21" || r.DueOn != "2027-01-22" {
		t.Fatalf("unexpected year-turn result: %+v", r)
	}
}
