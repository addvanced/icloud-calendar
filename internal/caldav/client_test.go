package caldav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	principalResponse = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/</D:href><D:propstat><D:prop><D:current-user-principal><D:href>/p/</D:href></D:current-user-principal></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
	homeSetResponse   = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:response><D:href>/p/</D:href><D:propstat><D:prop><C:calendar-home-set><D:href>/calendars/</D:href></C:calendar-home-set></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
	calendarsResponse = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:response><D:href>/calendars/1/</D:href><D:propstat><D:prop><D:displayname>Private</D:displayname><D:sync-token>tok1</D:sync-token><D:resourcetype><C:calendar/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
	queryResponse     = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:response><D:href>/calendars/1/e.ics</D:href><D:propstat><D:prop><D:getetag>e1</D:getetag><C:calendar-data>BEGIN:VCALENDAR&#13;&#10;BEGIN:VEVENT&#13;&#10;UID:u1&#13;&#10;SUMMARY:Mock&#13;&#10;DTSTART:20260521T100000Z&#13;&#10;DTEND:20260521T110000Z&#13;&#10;END:VEVENT&#13;&#10;END:VCALENDAR&#13;&#10;</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
	syncResponse      = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:sync-token>tok2</D:sync-token><D:response><D:href>/calendars/1/e.ics</D:href><D:propstat><D:prop><D:getetag>e2</D:getetag></D:prop><D:status>HTTP/2.0 200</D:status></D:propstat></D:response><D:response><D:href>/calendars/1/deleted.ics</D:href><D:propstat><D:prop></D:prop><D:status>HTTP/2.0 404</D:status></D:propstat></D:response></D:multistatus>`
	multiGetResponse  = `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:response><D:href>/calendars/1/e.ics</D:href><D:propstat><D:prop><D:getetag>e2</D:getetag><C:calendar-data>BEGIN:VCALENDAR&#13;&#10;BEGIN:VEVENT&#13;&#10;UID:u1&#13;&#10;SUMMARY:Mock2&#13;&#10;DTSTART:20260521T100000Z&#13;&#10;DTEND:20260521T110000Z&#13;&#10;END:VEVENT&#13;&#10;END:VCALENDAR&#13;&#10;</C:calendar-data></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`
)

func TestDiscover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "PROPFIND" && r.URL.Path == "/":
			_, _ = w.Write([]byte(principalResponse))
		case r.Method == "PROPFIND" && r.URL.Path == "/p/":
			_, _ = w.Write([]byte(homeSetResponse))
		case r.Method == "PROPFIND" && r.URL.Path == "/calendars/":
			_, _ = w.Write([]byte(calendarsResponse))
		default:
			unexpectedRequest(t, w, r, "")
		}
	}))
	defer srv.Close()

	cals, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 {
		t.Fatalf("len(cals) = %d, want 1: %+v", len(cals), cals)
	}
	if cals[0].URL != "/calendars/1/" {
		t.Errorf("URL = %q, want %q", cals[0].URL, "/calendars/1/")
	}
	if cals[0].Name != "Private" {
		t.Errorf("Name = %q, want %q", cals[0].Name, "Private")
	}
	if cals[0].SyncToken != "tok1" {
		t.Errorf("SyncToken = %q, want %q", cals[0].SyncToken, "tok1")
	}
}

func TestQueryObjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readReq(t, r)
		if r.Method != "REPORT" || r.URL.Path != "/calendars/1/" || !strings.Contains(body, "calendar-query") {
			unexpectedRequest(t, w, r, body)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(queryResponse))
	}))
	defer srv.Close()

	objs, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).QueryObjects(context.Background(), "/calendars/1/", time.Now().AddDate(-1, 0, 0), time.Now().AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 {
		t.Fatalf("len(objs) = %d, want 1: %+v", len(objs), objs)
	}
	if objs[0].Href != "/calendars/1/e.ics" {
		t.Errorf("Href = %q, want %q", objs[0].Href, "/calendars/1/e.ics")
	}
	if objs[0].ETag != "e1" {
		t.Errorf("ETag = %q, want %q", objs[0].ETag, "e1")
	}
	if !strings.Contains(objs[0].Raw, "Mock") {
		t.Errorf("Raw = %q, want to contain Mock", objs[0].Raw)
	}
}

func TestSyncCollection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readReq(t, r)
		if r.Method != "REPORT" || r.URL.Path != "/calendars/1/" || !strings.Contains(body, "sync-collection") {
			unexpectedRequest(t, w, r, body)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(syncResponse))
	}))
	defer srv.Close()

	changes, next, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).SyncCollection(context.Background(), "/calendars/1/", "tok1")
	if err != nil {
		t.Fatal(err)
	}
	if next != "tok2" {
		t.Errorf("next = %q, want %q", next, "tok2")
	}
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2: %+v", len(changes), changes)
	}
	if changes[0].Href != "/calendars/1/e.ics" || changes[0].ETag != "e2" || changes[0].Deleted {
		t.Errorf("change[0] = %+v, want updated e.ics", changes[0])
	}
	if changes[1].Href != "/calendars/1/deleted.ics" || !changes[1].Deleted {
		t.Errorf("change[1] = %+v, want deleted.ics marked deleted", changes[1])
	}
}

func TestMultiGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := readReq(t, r)
		if r.Method != "REPORT" || r.URL.Path != "/calendars/1/" || !strings.Contains(body, "calendar-multiget") || !strings.Contains(body, "<D:href>/calendars/1/e.ics</D:href>") {
			unexpectedRequest(t, w, r, body)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(multiGetResponse))
	}))
	defer srv.Close()

	objs, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).MultiGet(context.Background(), "/calendars/1/", []string{"/calendars/1/e.ics"})
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 {
		t.Fatalf("len(objs) = %d, want 1: %+v", len(objs), objs)
	}
	if objs[0].ETag != "e2" {
		t.Errorf("ETag = %q, want %q", objs[0].ETag, "e2")
	}
	if !strings.Contains(objs[0].Raw, "Mock2") {
		t.Errorf("Raw = %q, want to contain Mock2", objs[0].Raw)
	}
}

func unexpectedRequest(t testing.TB, w http.ResponseWriter, r *http.Request, body string) {
	t.Helper()
	if body == "" {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	} else {
		t.Errorf("unexpected %s %s body=%s", r.Method, r.URL.Path, body)
	}
	http.Error(w, "unexpected request", http.StatusInternalServerError)
}

func readReq(t testing.TB, r *http.Request) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(b)
}

func TestRetryTransientHTTP(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(principalResponse))
	}))
	defer srv.Close()

	principal, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).CurrentUserPrincipal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if principal != "/p/" {
		t.Errorf("principal = %q, want %q", principal, "/p/")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestIsInvalidSyncToken(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{http.StatusForbidden, true},
		{http.StatusConflict, true},
		{http.StatusGone, true},
		{http.StatusPreconditionFailed, true},
		{http.StatusOK, false},
		{http.StatusTooManyRequests, false},
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
	}
	for _, tt := range tests {
		err := &HTTPError{StatusCode: tt.code}
		if got := IsInvalidSyncToken(err); got != tt.want {
			t.Errorf("IsInvalidSyncToken(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestDiscoverPrincipalNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/</D:href><D:propstat><D:prop></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`))
	}))
	defer srv.Close()

	_, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).Discover(context.Background())
	if err == nil || !strings.Contains(err.Error(), "principal not found") {
		t.Fatalf("err = %v, want principal not found", err)
	}
}

func TestHTTPErrorReturnedForAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).CurrentUserPrincipal(context.Background())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %v, want HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusUnauthorized)
	}
}

func TestMalformedXMLReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<D:multistatus>`))
	}))
	defer srv.Close()

	_, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).CurrentUserPrincipal(context.Background())
	if err == nil {
		t.Fatal("expected malformed XML error")
	}
}

func TestContextCancellationStopsRetry(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		cancel()
		http.Error(w, "temporary", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := NewWithHTTP(srv.URL, "u", "p", srv.Client()).CurrentUserPrincipal(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestPathJoinPreservesTrailingSlash(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(queryResponse))
	}))
	defer srv.Close()

	c := NewWithHTTP(srv.URL, "u", "p", srv.Client())
	if _, err := c.QueryObjects(context.Background(), "/calendars/1/", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.QueryObjects(context.Background(), "/calendars/1/e.ics", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/calendars/1/", "/calendars/1/e.ics"}
	if len(got) != len(want) {
		t.Fatalf("got paths %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
