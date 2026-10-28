package scheduler

import (
	"testing"
	"time"

	"github.com/warmbly/warmbly/internal/models"
)

func weekdaysWindows(start, end int) models.ScheduleWindows {
	var sw models.ScheduleWindows
	for wd := time.Monday; wd <= time.Friday; wd++ {
		sw[wd] = []models.TimeInterval{{Start: start, End: end}}
	}
	return sw
}

// inWindow reports whether t falls inside one of sw's intervals on its own local day.
func inWindow(t time.Time, sw models.ScheduleWindows, tz *time.Location) bool {
	local := t.In(tz)
	minute := local.Hour()*60 + local.Minute()
	for _, iv := range sw[local.Weekday()] {
		if minute >= iv.Start && minute < iv.End {
			return true
		}
	}
	return false
}

// The weekend a zone leaves daylight saving time has a 25-hour day, which the
// slot search must walk past like any other day (issue #1044).
func TestNextScheduleSlotAcrossDaylightSavingChanges(t *testing.T) {
	sw := weekdaysWindows(8*60, 18*60)
	tests := []struct {
		zone string
		from time.Time
		want string // local wall time of the slot
	}{
		{"Europe/Madrid", time.Date(2026, 10, 23, 17, 30, 0, 0, time.UTC), "2026-10-26 08:00"},
		{"Europe/Madrid", time.Date(2026, 10, 24, 0, 10, 0, 0, time.UTC), "2026-10-26 08:00"},
		{"Europe/Madrid", time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), "2026-10-26 08:00"},
		{"Europe/Bucharest", time.Date(2026, 10, 24, 1, 30, 0, 0, time.UTC), "2026-10-26 08:00"},
		{"America/Detroit", time.Date(2026, 10, 31, 3, 0, 0, 0, time.UTC), "2026-11-02 08:00"},
		{"America/Chicago", time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC), "2026-11-02 08:00"},
		{"Europe/Madrid", time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC), "2026-03-30 08:00"},
		{"Europe/Madrid", time.Date(2026, 10, 17, 0, 10, 0, 0, time.UTC), "2026-10-19 08:00"},
	}
	for _, tt := range tests {
		tz, err := time.LoadLocation(tt.zone)
		if err != nil {
			t.Fatal(err)
		}
		got := nextScheduleSlot(tt.from, sw, tz).In(tz).Format("2006-01-02 15:04")
		if got != tt.want {
			t.Errorf("%s from %s: got %s, want %s", tt.zone, tt.from.Format(time.RFC3339), got, tt.want)
		}
	}
}

// A window that opens at midnight, or inside the hour a zone skips, still
// resolves to an instant inside that window and never to one before from.
func TestNextScheduleSlotWindowStartInSkippedTime(t *testing.T) {
	var sw models.ScheduleWindows
	for wd := range sw {
		sw[wd] = []models.TimeInterval{{Start: 0, End: 60}, {Start: 60 + 45, End: 2 * 60}, {Start: 2*60 + 30, End: 4 * 60}}
	}
	for _, zone := range []string{"America/Santiago", "America/Havana", "Atlantic/Azores", "America/New_York", "Europe/Madrid", "Australia/Lord_Howe"} {
		tz, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		for from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); from.Year() == 2026; from = from.Add(17 * time.Minute) {
			got := nextScheduleSlot(from, sw, tz)
			if got.Before(from) || got.Sub(from) > 26*time.Hour || !inWindow(got, sw, tz) {
				t.Fatalf("%s from %s: got %s", zone, from.In(tz).Format(time.RFC3339), got.In(tz).Format(time.RFC3339))
			}
		}
	}
}

// Every daylight-saving transition in a year, in zones that change at
// midnight, in the early hours, by half an hour, and in both hemispheres.
func TestNextScheduleSlotSweepsTimeZones(t *testing.T) {
	sw := weekdaysWindows(8*60, 18*60)
	zones := []string{
		"UTC", "Europe/Madrid", "Europe/London", "Europe/Bucharest", "America/New_York",
		"America/Chicago", "America/Santiago", "America/Havana", "America/Asuncion",
		"Atlantic/Azores", "Australia/Sydney", "Australia/Lord_Howe", "Pacific/Auckland",
		"Asia/Tehran", "Africa/Cairo",
	}
	for _, zone := range zones {
		tz, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		for from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); from.Year() == 2026; from = from.Add(29 * time.Minute) {
			got := nextScheduleSlot(from, sw, tz)
			if got.Before(from) || got.Sub(from) > 4*24*time.Hour || !inWindow(got, sw, tz) {
				t.Fatalf("%s from %s: got %s", zone, from.In(tz).Format(time.RFC3339), got.In(tz).Format(time.RFC3339))
			}
		}
	}
}

// A schedule with days but no usable window defers past the search horizon; only an empty one means "any time".
func TestNextScheduleSlotFailsClosed(t *testing.T) {
	tz, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 10, 23, 17, 30, 0, 0, time.UTC)
	var unusable models.ScheduleWindows
	for wd := range unusable {
		unusable[wd] = []models.TimeInterval{{Start: 10 * 60, End: 10 * 60}}
	}
	if got, want := nextScheduleSlot(from, unusable, tz), from.AddDate(0, 0, 8); !got.Equal(want) {
		t.Errorf("no usable window: got %s, want %s", got, want)
	}
	if got := nextScheduleSlot(from, models.ScheduleWindows{}, tz); !got.Equal(from) {
		t.Errorf("empty schedule: got %s, want %s", got, from)
	}
}
