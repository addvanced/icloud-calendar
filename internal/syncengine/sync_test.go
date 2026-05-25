package syncengine

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/addvanced/icloud-calendar/internal/caldav"
	"github.com/addvanced/icloud-calendar/internal/db"
	"github.com/addvanced/icloud-calendar/internal/model"
)

type fakeClient struct {
	calendars []model.Calendar
	objects   []caldav.Object
	changes   []model.SyncChange
	multi     []caldav.Object
	next      string

	discoverErr error
	queryErr    error
	syncErr     error
	multiErr    error
}

func (f fakeClient) Discover(context.Context) ([]model.Calendar, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	return f.calendars, nil
}

func (f fakeClient) QueryObjects(context.Context, string, time.Time, time.Time) ([]caldav.Object, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.objects, nil
}

func (f fakeClient) SyncCollection(context.Context, string, string) ([]model.SyncChange, string, error) {
	if f.syncErr != nil {
		return nil, "", f.syncErr
	}
	return f.changes, f.next, nil
}

func (f fakeClient) MultiGet(context.Context, string, []string) ([]caldav.Object, error) {
	if f.multiErr != nil {
		return nil, f.multiErr
	}
	return f.multi, nil
}

const (
	rawEvent        = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:u1\r\nSUMMARY:Synced\r\nDTSTART:20260521T100000Z\r\nDTEND:20260521T110000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	rawEvent2       = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:u2\r\nSUMMARY:Changed\r\nDTSTART:20260522T100000Z\r\nDTEND:20260522T110000Z\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	rawNoVEVENT     = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"
	fixedNowRFC3339 = "2026-05-21T12:00:00Z"
)

func TestSyncFullThenIncremental(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cal := model.Calendar{URL: "/cal/", Name: "Private", SyncToken: "tok1"}
	eng := Engine{
		Store: store,
		Client: fakeClient{
			calendars: []model.Calendar{cal},
			objects:   []caldav.Object{{Href: "/cal/1.ics", ETag: "e1", Raw: rawEvent}},
		},
		Location: time.UTC,
		Now:      fixedNow,
	}
	res, err := eng.Sync(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Upserted != 1 || !res.Full {
		t.Fatalf("bad full result %+v", res)
	}
	events, err := store.Search(ctx, "Synced", "")
	if err != nil || len(events) != 1 {
		t.Fatalf("bad full search %d %v", len(events), err)
	}
	eng.Client = fakeClient{
		calendars: []model.Calendar{cal},
		changes: []model.SyncChange{
			{Href: "/cal/1.ics", Deleted: true},
			{Href: "/cal/2.ics", ETag: "e2"},
		},
		multi: []caldav.Object{{Href: "/cal/2.ics", ETag: "e2", Raw: rawEvent2}},
		next:  "tok2",
	}
	res, err = eng.Sync(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 || res.Upserted != 1 {
		t.Fatalf("bad inc result %+v", res)
	}
	events, err = store.Search(ctx, "Changed", "")
	if err != nil || len(events) != 1 {
		t.Fatalf("bad inc search %d %v", len(events), err)
	}
}

type invalidTokenClient struct{ fakeClient }

func (f invalidTokenClient) SyncCollection(context.Context, string, string) ([]model.SyncChange, string, error) {
	return nil, "", &caldav.HTTPError{StatusCode: http.StatusGone}
}

func TestSyncInvalidTokenFallsBackToFull(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cal := model.Calendar{URL: "/cal/", Name: "Private", SyncToken: "tok1"}
	if err := store.UpsertCalendar(ctx, cal); err != nil {
		t.Fatal(err)
	}
	client := invalidTokenClient{fakeClient: fakeClient{
		calendars: []model.Calendar{cal},
		objects:   []caldav.Object{{Href: "/cal/1.ics", ETag: "e1", Raw: rawEvent}},
	}}
	eng := Engine{Store: store, Client: client, Location: time.UTC, Now: fixedNow}
	res, err := eng.Sync(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Full || res.Upserted != 1 {
		t.Fatalf("expected fallback to preserve requested-full flag, got %+v", res)
	}
}

type tokenCheckingClient struct{ token string }

func (c *tokenCheckingClient) Discover(context.Context) ([]model.Calendar, error) {
	return []model.Calendar{{URL: "/cal/", Name: "Private", SyncToken: "server-new"}}, nil
}

func (c *tokenCheckingClient) QueryObjects(context.Context, string, time.Time, time.Time) ([]caldav.Object, error) {
	return nil, nil
}

func (c *tokenCheckingClient) SyncCollection(_ context.Context, _ string, token string) ([]model.SyncChange, string, error) {
	c.token = token
	return nil, "server-new", nil
}

func (c *tokenCheckingClient) MultiGet(context.Context, string, []string) ([]caldav.Object, error) {
	return nil, nil
}

func TestSyncUsesStoredTokenNotDiscoveryToken(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	if err := store.UpsertCalendar(ctx, model.Calendar{URL: "/cal/", Name: "Private", SyncToken: "stored-old"}); err != nil {
		t.Fatal(err)
	}
	client := &tokenCheckingClient{}
	eng := Engine{Store: store, Client: client, Location: time.UTC}
	_, err := eng.Sync(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if client.token != "stored-old" {
		t.Fatalf("used token %q, want stored-old", client.token)
	}
}

func TestSyncRequiresStoreAndClient(t *testing.T) {
	tests := []struct {
		name string
		eng  Engine
	}{
		{name: "no store", eng: Engine{Client: fakeClient{}}},
		{name: "no client", eng: Engine{Store: openTestStore(t)}},
		{name: "neither", eng: Engine{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.eng.Sync(context.Background(), false)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), "store and client are required") {
				t.Fatalf("err = %v, want store/client validation", err)
			}
		})
	}
}

func TestSyncFailsWhenLockAlreadyHeld(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	release, err := store.AcquireLock(ctx, "sync")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(context.Background()); err != nil {
			t.Errorf("release lock: %v", err)
		}
	}()

	eng := Engine{Store: store, Client: fakeClient{}, Location: time.UTC}
	_, err = eng.Sync(ctx, false)
	if err == nil {
		t.Fatal("expected lock contention error")
	}
	if !strings.Contains(err.Error(), "sync is already running") {
		t.Fatalf("err = %v, want lock contention", err)
	}
}

func TestSyncRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	eng := Engine{Store: openTestStore(t), Client: fakeClient{}, Location: time.UTC}
	_, err := eng.Sync(ctx, false)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSyncSkipsObjectsWithoutEvents(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cal := model.Calendar{URL: "/cal/", Name: "Private", SyncToken: "tok1"}
	eng := Engine{
		Store: store,
		Client: fakeClient{
			calendars: []model.Calendar{cal},
			objects:   []caldav.Object{{Href: "/cal/no-event.ics", ETag: "e1", Raw: rawNoVEVENT}},
		},
		Location: time.UTC,
		Now:      fixedNow,
	}
	_, err := eng.Sync(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	_, events, _, err := store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events = %d, want 0", events)
	}
}

func TestSyncWithNoCalendars(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	eng := Engine{Store: store, Client: fakeClient{}, Location: time.UTC, Now: fixedNow}
	res, err := eng.Sync(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Calendars != 0 || res.Upserted != 0 || res.Deleted != 0 {
		t.Fatalf("result = %+v, want no-op", res)
	}
	calendars, events, _, err := store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if calendars != 0 || events != 0 {
		t.Fatalf("status calendars=%d events=%d, want 0/0", calendars, events)
	}
}

func TestSyncPropagatesClientErrors(t *testing.T) {
	ctx := context.Background()
	cal := model.Calendar{URL: "/cal/", Name: "Private", SyncToken: "stored-token"}
	testErr := errors.New("client failed")
	tests := []struct {
		name       string
		client     fakeClient
		seedStored bool
		full       bool
	}{
		{
			name:   "discover",
			client: fakeClient{discoverErr: testErr},
		},
		{
			name:   "full query",
			client: fakeClient{calendars: []model.Calendar{cal}, queryErr: testErr},
			full:   true,
		},
		{
			name:       "incremental sync",
			client:     fakeClient{calendars: []model.Calendar{cal}, syncErr: testErr},
			seedStored: true,
		},
		{
			name: "multiget",
			client: fakeClient{
				calendars: []model.Calendar{cal},
				changes:   []model.SyncChange{{Href: "/cal/1.ics", ETag: "e1"}},
				multiErr:  testErr,
			},
			seedStored: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestStore(t)
			if tt.seedStored {
				if err := store.UpsertCalendar(ctx, cal); err != nil {
					t.Fatal(err)
				}
			}
			eng := Engine{Store: store, Client: tt.client, Location: time.UTC, Now: fixedNow}
			_, err := eng.Sync(ctx, tt.full)
			if !errors.Is(err, testErr) {
				t.Fatalf("err = %v, want %v", err, testErr)
			}
		})
	}
}

func openTestStore(t testing.TB) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})
	return store
}

func fixedNow() time.Time {
	t, err := time.Parse(time.RFC3339, fixedNowRFC3339)
	if err != nil {
		panic(err)
	}
	return t
}
