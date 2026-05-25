package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/addvanced/icloud-calendar/internal/model"
)

func TestStoreCRUDAndSearch(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("bad perms %v %v", st, err)
	}
	syncAt := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	cal := model.Calendar{URL: "/cal/1/", Name: "Private", SyncToken: "tok", LastSyncAt: syncAt}
	if err := s.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	e := model.Event{UID: "u1", Href: "/cal/1/e.ics", CalendarURL: cal.URL, ETag: "e1", ICalRaw: "raw", Summary: "Dentist", Location: "Kolding", StartTime: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC), EndTime: time.Date(2026, 5, 21, 11, 0, 0, 0, time.UTC), SyncedAt: time.Now()}
	if err := s.UpsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	found, err := s.Search(ctx, "Dentist", "title")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].UID != "u1" {
		t.Fatalf("bad search: %+v", found)
	}
	if _, err := s.Search(ctx, "Dentist", "bogus"); err == nil {
		t.Fatalf("expected invalid field error")
	}
	ev, ok, err := s.EventByID(ctx, "u1")
	if err != nil || !ok || ev.UID != "u1" {
		t.Fatalf("bad event by id: %+v %t %v", ev, ok, err)
	}
	storedCal, ok, err := s.CalendarByURL(ctx, cal.URL)
	if err != nil || !ok || !storedCal.LastSyncAt.Equal(syncAt) {
		t.Fatalf("bad calendar lookup: %+v %t %v", storedCal, ok, err)
	}
	cc, ec, lastSync, err := s.Status(ctx)
	if err != nil || cc != 1 || ec != 1 || !lastSync.Equal(syncAt) {
		t.Fatalf("bad status: %d %d %s %v", cc, ec, lastSync, err)
	}
	ranged, err := s.EventsInRange(ctx, e.StartTime.Add(-time.Hour), e.EndTime.Add(time.Hour), "Private")
	if err != nil {
		t.Fatal(err)
	}
	if len(ranged) != 1 {
		t.Fatalf("bad range count %d", len(ranged))
	}
	if err := s.DeleteEvent(ctx, cal.URL, e.Href); err != nil {
		t.Fatal(err)
	}
	found, err = s.Search(ctx, "Dentist", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("expected deleted from FTS, got %d", len(found))
	}
}

func TestAcquireLock(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release, err := s.AcquireLock(ctx, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireLock(ctx, "sync"); err == nil {
		t.Fatalf("expected duplicate lock error")
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	if release, err = s.AcquireLock(ctx, "sync"); err != nil {
		t.Fatal(err)
	}
	_ = release(ctx)
}

func TestDefaultPathAndOpenDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".cache", "icalendar", "events.db")
	if path != want {
		t.Fatalf("DefaultPath = %q, want %q", path, want)
	}
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if st, err := os.Stat(want); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("default database perms = %v %v, want 0600", st, err)
	}
}

func TestStoreRejectsBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected permission error")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("err = %v, want permission error", err)
	}
}

func TestCalendarAndEventLookups(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cc, ec, lastSync, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cc != 0 || ec != 0 || !lastSync.IsZero() {
		t.Fatalf("empty status calendars=%d events=%d lastSync=%v, want zero values", cc, ec, lastSync)
	}
	if _, ok, err := s.CalendarByURL(ctx, "/missing/"); err != nil || ok {
		t.Fatalf("missing calendar ok=%t err=%v, want false/nil", ok, err)
	}
	if _, ok, err := s.EventByID(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing event ok=%t err=%v, want false/nil", ok, err)
	}

	firstSync := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	secondSync := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	calendars := []model.Calendar{
		{URL: "/cal/b/", Name: "Work", SyncToken: "tok-b", LastSyncAt: secondSync, ETag: "etag-b"},
		{URL: "/cal/a/", Name: "Private", SyncToken: "tok-a", LastSyncAt: firstSync, ETag: "etag-a"},
	}
	for _, cal := range calendars {
		if err := s.UpsertCalendar(ctx, cal); err != nil {
			t.Fatal(err)
		}
	}
	gotCals, err := s.Calendars(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotCals) != 2 {
		t.Fatalf("len(calendars) = %d, want 2: %+v", len(gotCals), gotCals)
	}
	if gotCals[0].Name != "Private" || gotCals[1].Name != "Work" {
		t.Fatalf("calendar order = %+v, want by name", gotCals)
	}
	if gotCals[1].SyncToken != "tok-b" || gotCals[1].ETag != "etag-b" || !gotCals[1].LastSyncAt.Equal(secondSync) {
		t.Fatalf("calendar fields not round-tripped: %+v", gotCals[1])
	}

	e := testEvent("/cal/a/", "u1", "/cal/a/e.ics", time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))
	if err := s.UpsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	byHref, ok, err := s.EventByID(ctx, e.Href)
	if err != nil || !ok || byHref.UID != e.UID {
		t.Fatalf("EventByID href = %+v ok=%t err=%v, want u1", byHref, ok, err)
	}
}

func TestSearchFields(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cal := model.Calendar{URL: "/cal/1/", Name: "Private"}
	if err := s.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	e := testEvent(cal.URL, "u1", "/cal/1/e.ics", time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))
	e.Summary = "Dentist"
	e.Location = "Kolding"
	e.Description = "Annual checkup"
	if err := s.UpsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		field string
		query string
	}{
		{field: "summary", query: "Dentist"},
		{field: "title", query: "Dentist"},
		{field: "location", query: "Kolding"},
		{field: "description", query: "Annual"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			found, err := s.Search(ctx, tc.query, tc.field)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 1 || found[0].UID != e.UID {
				t.Fatalf("found = %+v, want u1", found)
			}
		})
	}
}

func TestDeleteCalendarCascadesToEvents(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cal := model.Calendar{URL: "/cal/1/", Name: "Private"}
	if err := s.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertEvent(ctx, testEvent(cal.URL, "u1", "/cal/1/e.ics", time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM calendars WHERE url=?`, cal.URL); err != nil {
		t.Fatal(err)
	}
	cc, ec, _, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cc != 0 || ec != 0 {
		t.Fatalf("status calendars=%d events=%d, want 0/0", cc, ec)
	}
}

func TestAcquireLockClearsStaleLocks(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	staleTime := time.Now().Add(-31 * time.Minute).Round(0)
	if _, err := s.DB().ExecContext(ctx, `INSERT INTO app_locks(name, locked_at) VALUES(?, ?)`, "sync", staleTime); err != nil {
		t.Fatal(err)
	}
	release, err := s.AcquireLock(ctx, "sync")
	if err != nil {
		t.Fatalf("expected lock acquisition after stale cleanup, got %v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestEventsInRangeOverlapEdgeCases(t *testing.T) {
	ctx := context.Background()
	queryFrom := time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	queryTo := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		allDay bool
		start  time.Time
		end    time.Time
		want   bool
	}{
		{
			name:   "all-day event on query day",
			allDay: true,
			start:  queryFrom,
			end:    queryTo,
			want:   true,
		},
		{
			name:   "all-day event spanning multiple days",
			allDay: true,
			start:  queryFrom.AddDate(0, 0, -1),
			end:    queryTo.AddDate(0, 0, 1),
			want:   true,
		},
		{
			name:   "all-day event ending at query start is excluded",
			allDay: true,
			start:  queryFrom.AddDate(0, 0, -1),
			end:    queryFrom,
			want:   false,
		},
		{
			name:   "all-day event starting at query end is excluded",
			allDay: true,
			start:  queryTo,
			end:    queryTo.AddDate(0, 0, 1),
			want:   false,
		},
		{
			name:  "timed event inside range",
			start: queryFrom.Add(10 * time.Hour),
			end:   queryFrom.Add(11 * time.Hour),
			want:  true,
		},
		{
			name:  "timed event starts before range and ends inside range",
			start: queryFrom.Add(-1 * time.Hour),
			end:   queryFrom.Add(1 * time.Hour),
			want:  true,
		},
		{
			name:  "timed event starts inside range and ends after range",
			start: queryTo.Add(-1 * time.Hour),
			end:   queryTo.Add(1 * time.Hour),
			want:  true,
		},
		{
			name:  "timed event spans whole range",
			start: queryFrom.Add(-1 * time.Hour),
			end:   queryTo.Add(1 * time.Hour),
			want:  true,
		},
		{
			name:  "timed event ending at query start is excluded",
			start: queryFrom.Add(-1 * time.Hour),
			end:   queryFrom,
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "events.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			cal := model.Calendar{URL: "/cal/1/", Name: "Private", SyncToken: "tok"}
			if err := s.UpsertCalendar(ctx, cal); err != nil {
				t.Fatal(err)
			}
			e := model.Event{
				UID:         "u1",
				Href:        "/cal/1/e.ics",
				CalendarURL: cal.URL,
				ETag:        "e1",
				ICalRaw:     "raw",
				Summary:     tc.name,
				StartTime:   tc.start,
				EndTime:     tc.end,
				AllDay:      tc.allDay,
				SyncedAt:    time.Now(),
			}
			if err := s.UpsertEvent(ctx, e); err != nil {
				t.Fatal(err)
			}

			events, err := s.EventsInRange(ctx, queryFrom, queryTo, "private")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want {
				if len(events) != 1 || events[0].UID != e.UID {
					t.Fatalf("expected one matching event, got %+v", events)
				}
				return
			}
			if len(events) != 0 {
				t.Fatalf("expected no matching events, got %+v", events)
			}
		})
	}
}

func TestEventsInRangeCaseInsensitiveCalendar(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cal := model.Calendar{URL: "/cal/1/", Name: "Private"}
	if err := s.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	if err := s.UpsertEvent(ctx, testEvent(cal.URL, "u1", "/cal/1/e.ics", start)); err != nil {
		t.Fatal(err)
	}
	for _, calendarName := range []string{"Private", "private", "PRIVATE", "PrIvAtE"} {
		t.Run(calendarName, func(t *testing.T) {
			events, err := s.EventsInRange(ctx, start.Add(-time.Hour), start.Add(2*time.Hour), calendarName)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].UID != "u1" {
				t.Fatalf("events = %+v, want u1", events)
			}
		})
	}
}

func TestEventsInRangeOrderedByStartTime(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cal := model.Calendar{URL: "/cal/1/", Name: "Private"}
	if err := s.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	inputs := []struct {
		uid    string
		href   string
		offset time.Duration
	}{
		{uid: "u3", href: "/cal/1/3.ics", offset: 3 * time.Hour},
		{uid: "u1", href: "/cal/1/1.ics", offset: time.Hour},
		{uid: "u4", href: "/cal/1/4.ics", offset: 4 * time.Hour},
		{uid: "u2", href: "/cal/1/2.ics", offset: 2 * time.Hour},
		{uid: "u0", href: "/cal/1/0.ics", offset: 0},
	}
	for _, in := range inputs {
		if err := s.UpsertEvent(ctx, testEvent(cal.URL, in.uid, in.href, base.Add(in.offset))); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.EventsInRange(ctx, base.Add(-time.Hour), base.Add(6*time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	wantUIDs := []string{"u0", "u1", "u2", "u3", "u4"}
	if len(events) != len(wantUIDs) {
		t.Fatalf("len(events) = %d, want %d: %+v", len(events), len(wantUIDs), events)
	}
	for i, want := range wantUIDs {
		if events[i].UID != want {
			t.Errorf("events[%d].UID = %q, want %q", i, events[i].UID, want)
		}
	}
}

func TestOpenRejectsNonDirectoryParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(filepath.Join(parent, "events.db"))
	if err == nil {
		t.Fatal("expected error for non-directory parent")
	}
}

func TestAcquireLockWithCanceledContext(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.AcquireLock(ctx, "sync")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func testEvent(calendarURL, uid, href string, start time.Time) model.Event {
	return model.Event{
		UID:         uid,
		Href:        href,
		CalendarURL: calendarURL,
		ETag:        "e1",
		ICalRaw:     "raw",
		Summary:     uid,
		StartTime:   start,
		EndTime:     start.Add(time.Hour),
		SyncedAt:    start,
	}
}
