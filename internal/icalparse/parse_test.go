package icalparse

import (
	"strings"
	"testing"
	"time"

	"github.com/addvanced/icloud-calendar/internal/model"
)

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(t *testing.T, ev model.Event, raw string)
	}{
		{
			name: "timed event with all fields",
			raw: eventICS(
				"UID:abc123",
				"SUMMARY:Test event",
				"LOCATION:Kolding",
				"DESCRIPTION:Hello",
				"DTSTART:20260521T100000Z",
				"DTEND:20260521T110000Z",
				"LAST-MODIFIED:20260520T090000Z",
				"RRULE:FREQ=WEEKLY;COUNT=2",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "abc123")
				assertString(t, "Summary", ev.Summary, "Test event")
				assertString(t, "Location", ev.Location, "Kolding")
				assertString(t, "Description", ev.Description, "Hello")
				assertTime(t, "StartTime", ev.StartTime, time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))
				assertTime(t, "EndTime", ev.EndTime, time.Date(2026, 5, 21, 11, 0, 0, 0, time.UTC))
				assertTime(t, "LastModified", ev.LastModified, time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC))
				assertString(t, "RecurrenceInfo", ev.RecurrenceInfo, "RRULE:FREQ=WEEKLY;COUNT=2")
				if ev.AllDay {
					t.Error("AllDay = true, want false")
				}
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
			},
		},
		{
			name: "all-day event",
			raw: eventICS(
				"UID:day1",
				"SUMMARY:All day",
				"DTSTART;VALUE=DATE:20260521",
				"DTEND;VALUE=DATE:20260522",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "day1")
				if !ev.AllDay {
					t.Error("AllDay = false, want true")
				}
				assertDuration(t, ev, 24*time.Hour)
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
			},
		},
		{
			name: "event without optional text fields",
			raw: eventICS(
				"UID:minimal1",
				"DTSTART:20260521T100000Z",
				"DTEND:20260521T110000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "minimal1")
				assertString(t, "Summary", ev.Summary, "")
				assertString(t, "Location", ev.Location, "")
				assertString(t, "Description", ev.Description, "")
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
			},
		},
		{
			name: "timed event without DTEND defaults to zero duration",
			raw: eventICS(
				"UID:no-end-timed",
				"DTSTART:20260521T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "no-end-timed")
				assertTime(t, "StartTime", ev.StartTime, time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))
				assertTime(t, "EndTime", ev.EndTime, ev.StartTime)
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
			},
		},
		{
			name: "all-day event without DTEND defaults to one day",
			raw: eventICS(
				"UID:allday-no-end",
				"DTSTART;VALUE=DATE:20260521",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "allday-no-end")
				if !ev.AllDay {
					t.Error("AllDay = false, want true")
				}
				assertDuration(t, ev, 24*time.Hour)
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
			},
		},
		{
			name: "minimum UID and DTSTART event",
			raw: eventICS(
				"UID:minimum",
				"DTSTART:20260521T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "minimum")
				assertTime(t, "StartTime", ev.StartTime, time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))
				assertTime(t, "EndTime", ev.EndTime, ev.StartTime)
			},
		},
		{
			name: "RDATE recurrence marker",
			raw: eventICS(
				"UID:rdate1",
				"DTSTART:20260521T100000Z",
				"RDATE:20260601T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "RecurrenceInfo", ev.RecurrenceInfo, "RDATE")
			},
		},
		{
			name: "EXDATE recurrence marker",
			raw: eventICS(
				"UID:exdate1",
				"DTSTART:20260521T100000Z",
				"EXDATE:20260528T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "RecurrenceInfo", ev.RecurrenceInfo, "EXDATE")
			},
		},
		{
			name: "RRULE with EXDATE recurrence markers",
			raw: eventICS(
				"UID:rrule-exdate1",
				"DTSTART:20260521T100000Z",
				"RRULE:FREQ=WEEKLY;COUNT=3",
				"EXDATE:20260528T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "RecurrenceInfo", ev.RecurrenceInfo, "RRULE:FREQ=WEEKLY;COUNT=3;EXDATE")
			},
		},
		{
			name: "RRULE with RDATE and EXDATE recurrence markers",
			raw: eventICS(
				"UID:rec1",
				"SUMMARY:Recurring",
				"DTSTART:20260521T100000Z",
				"DTEND:20260521T110000Z",
				"RRULE:FREQ=WEEKLY;COUNT=3",
				"RDATE:20260601T100000Z",
				"EXDATE:20260528T100000Z",
			),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "RecurrenceInfo", ev.RecurrenceInfo, "RRULE:FREQ=WEEKLY;COUNT=3;RDATE;EXDATE")
			},
		},
		{
			name:    "malformed iCalendar data returns error",
			raw:     "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:bad\r\nDTSTART:20260521T100000Z\r\n",
			wantErr: true,
		},
		{
			name: "empty VCALENDAR returns empty event with raw preserved",
			raw:  calendarICS(),
			check: func(t *testing.T, ev model.Event, raw string) {
				t.Helper()
				assertString(t, "UID", ev.UID, "")
				assertString(t, "Summary", ev.Summary, "")
				assertString(t, "ICalRaw", ev.ICalRaw, raw)
				if !ev.StartTime.IsZero() {
					t.Errorf("StartTime = %v, want zero", ev.StartTime)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseEvent(tt.raw, time.UTC)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEvent error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.check == nil {
				t.Fatal("test case missing check")
			}
			tt.check(t, ev, tt.raw)
		})
	}
}

func TestDefaultEndTime(t *testing.T) {
	start := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	assertTime(t, "timed default end", defaultEndTime(start, false), start)
	assertTime(t, "all-day default end", defaultEndTime(start, true), start.Add(24*time.Hour))
}

func TestParseEventInCopenhagenTime(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	raw := eventICS(
		"UID:copenhagen1",
		"SUMMARY:Floating Copenhagen time",
		"DTSTART:20260521T100000",
		"DTEND:20260521T110000",
	)
	ev, err := ParseEvent(raw, loc)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 5, 21, 10, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 5, 21, 11, 0, 0, 0, loc)
	assertTime(t, "StartTime", ev.StartTime, wantStart)
	assertTime(t, "EndTime", ev.EndTime, wantEnd)
	if ev.StartTime.Location() != loc {
		t.Errorf("StartTime location = %v, want %v", ev.StartTime.Location(), loc)
	}
}

func calendarICS(lines ...string) string {
	all := []string{"BEGIN:VCALENDAR", "VERSION:2.0"}
	all = append(all, lines...)
	all = append(all, "END:VCALENDAR")
	return strings.Join(all, "\r\n") + "\r\n"
}

func eventICS(lines ...string) string {
	eventLines := []string{"BEGIN:VEVENT"}
	eventLines = append(eventLines, lines...)
	eventLines = append(eventLines, "END:VEVENT")
	return calendarICS(eventLines...)
}

func assertString(t testing.TB, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertTime(t testing.TB, field string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}

func assertDuration(t testing.TB, ev model.Event, want time.Duration) {
	t.Helper()
	if got := ev.EndTime.Sub(ev.StartTime); got != want {
		t.Errorf("duration = %v, want %v; start=%v end=%v", got, want, ev.StartTime, ev.EndTime)
	}
}
