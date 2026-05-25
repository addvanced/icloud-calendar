package caldav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/addvanced/icloud-calendar/internal/model"
)

const DefaultEndpoint = "https://caldav.icloud.com"

type Client struct {
	http     *http.Client
	endpoint string
	username string
	password string
}

func New(username, password string) *Client {
	return &Client{http: &http.Client{Timeout: 45 * time.Second}, endpoint: DefaultEndpoint, username: username, password: password}
}

func NewWithHTTP(endpoint, username, password string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 45 * time.Second}
	}
	return &Client{http: hc, endpoint: endpoint, username: username, password: password}
}

type hrefValue struct {
	Href string `xml:"href"`
}
type multiStatus struct {
	Responses []response `xml:"response"`
	SyncToken string     `xml:"sync-token"`
}
type response struct {
	Href      string     `xml:"href"`
	PropStats []propStat `xml:"propstat"`
}
type propStat struct {
	Status string `xml:"status"`
	Prop   prop   `xml:"prop"`
}
type prop struct {
	CurrentUserPrincipal hrefValue    `xml:"current-user-principal"`
	CalendarHomeSet      hrefValue    `xml:"calendar-home-set"`
	DisplayName          string       `xml:"displayname"`
	SyncToken            string       `xml:"sync-token"`
	ResourceType         resourceType `xml:"resourcetype"`
	CalendarData         string       `xml:"calendar-data"`
	GetETag              string       `xml:"getetag"`
}
type resourceType struct {
	Calendar *struct{} `xml:"urn:ietf:params:xml:ns:caldav calendar"`
}

type Object struct {
	Href string
	ETag string
	Raw  string
}

type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s failed: HTTP %d", e.Method, e.Path, e.StatusCode)
}

func IsInvalidSyncToken(err error) bool {
	var httpErr *HTTPError
	return errors.As(err, &httpErr) && (httpErr.StatusCode == http.StatusForbidden || httpErr.StatusCode == http.StatusConflict || httpErr.StatusCode == http.StatusGone || httpErr.StatusCode == http.StatusPreconditionFailed)
}

func (c *Client) Discover(ctx context.Context) ([]model.Calendar, error) {
	principal, err := c.CurrentUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	home, err := c.CalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, err
	}
	return c.Calendars(ctx, home)
}

func (c *Client) CurrentUserPrincipal(ctx context.Context) (string, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop><D:current-user-principal/></D:prop></D:propfind>`
	ms, err := c.doXML(ctx, "PROPFIND", "/", "0", body)
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		for _, ps := range r.PropStats {
			if okStatus(ps.Status) && ps.Prop.CurrentUserPrincipal.Href != "" {
				return normalizePath(ps.Prop.CurrentUserPrincipal.Href), nil
			}
		}
	}
	return "", errors.New("principal not found")
}

func (c *Client) CalendarHomeSet(ctx context.Context, principal string) (string, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><C:calendar-home-set/></D:prop></D:propfind>`
	ms, err := c.doXML(ctx, "PROPFIND", principal, "0", body)
	if err != nil {
		return "", err
	}
	for _, r := range ms.Responses {
		for _, ps := range r.PropStats {
			if okStatus(ps.Status) && ps.Prop.CalendarHomeSet.Href != "" {
				return normalizePath(ps.Prop.CalendarHomeSet.Href), nil
			}
		}
	}
	return "", errors.New("calendar-home-set not found")
}

func (c *Client) Calendars(ctx context.Context, home string) ([]model.Calendar, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:displayname/><D:resourcetype/><D:sync-token/></D:prop></D:propfind>`
	ms, err := c.doXML(ctx, "PROPFIND", home, "1", body)
	if err != nil {
		return nil, err
	}
	var out []model.Calendar
	for _, r := range ms.Responses {
		cal := model.Calendar{URL: normalizePath(r.Href)}
		var isCalendar bool
		for _, ps := range r.PropStats {
			if !okStatus(ps.Status) {
				continue
			}
			if ps.Prop.DisplayName != "" {
				cal.Name = ps.Prop.DisplayName
			}
			if ps.Prop.SyncToken != "" {
				cal.SyncToken = ps.Prop.SyncToken
			}
			if ps.Prop.ResourceType.Calendar != nil {
				isCalendar = true
			}
		}
		if isCalendar && cal.Name != "" {
			out = append(out, cal)
		}
	}
	return out, nil
}

func (c *Client) QueryObjects(ctx context.Context, calendarPath string, from, to time.Time) ([]Object, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"><C:time-range start="` + from.UTC().Format("20060102T150405Z") + `" end="` + to.UTC().Format("20060102T150405Z") + `"/></C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`
	ms, err := c.doXML(ctx, "REPORT", calendarPath, "1", body)
	if err != nil {
		return nil, err
	}
	return objectsFromMultiStatus(ms), nil
}

func (c *Client) MultiGet(ctx context.Context, calendarPath string, hrefs []string) ([]Object, error) {
	var h strings.Builder
	for _, href := range hrefs {
		h.WriteString("<D:href>")
		h.WriteString(xmlEscape(href))
		h.WriteString("</D:href>")
	}
	body := `<?xml version="1.0" encoding="utf-8"?><C:calendar-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/><C:calendar-data/></D:prop>` + h.String() + `</C:calendar-multiget>`
	ms, err := c.doXML(ctx, "REPORT", calendarPath, "1", body)
	if err != nil {
		return nil, err
	}
	return objectsFromMultiStatus(ms), nil
}

func (c *Client) SyncCollection(ctx context.Context, calendarPath, token string) ([]model.SyncChange, string, error) {
	body := `<?xml version="1.0" encoding="utf-8"?><D:sync-collection xmlns:D="DAV:"><D:sync-token>` + xmlEscape(token) + `</D:sync-token><D:sync-level>1</D:sync-level><D:prop><D:getetag/></D:prop></D:sync-collection>`
	ms, err := c.doXML(ctx, "REPORT", calendarPath, "1", body)
	if err != nil {
		return nil, "", err
	}
	var changes []model.SyncChange
	for _, r := range ms.Responses {
		href := normalizePath(r.Href)
		if href == calendarPath {
			continue
		}
		ch := model.SyncChange{Href: href}
		for _, ps := range r.PropStats {
			code := statusCode(ps.Status)
			if code == http.StatusNotFound {
				ch.Deleted = true
			}
			if code == http.StatusOK {
				ch.ETag = ps.Prop.GetETag
			}
		}
		changes = append(changes, ch)
	}
	return changes, ms.SyncToken, nil
}

func objectsFromMultiStatus(ms *multiStatus) []Object {
	var out []Object
	for _, r := range ms.Responses {
		obj := Object{Href: normalizePath(r.Href)}
		for _, ps := range r.PropStats {
			if okStatus(ps.Status) {
				obj.ETag = ps.Prop.GetETag
				obj.Raw = ps.Prop.CalendarData
			}
		}
		if obj.Raw != "" {
			out = append(out, obj)
		}
	}
	return out
}

func (c *Client) doXML(ctx context.Context, method, path, depth, body string) (*multiStatus, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		ms, err := c.doXMLOnce(ctx, method, path, depth, body)
		if err == nil {
			return ms, nil
		}
		lastErr = err
		if !isTransient(err) || attempt == 2 {
			break
		}
		timer := time.NewTimer(time.Duration(250*(1<<attempt)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return nil, fmt.Errorf("retry cancelled: %w (last error: %v)", ctx.Err(), lastErr)
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (c *Client) doXMLOnce(ctx context.Context, method, path, depth, body string) (*multiStatus, error) {
	u, err := url.JoinPath(c.endpoint, path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, "/") && !strings.HasSuffix(u, "/") {
		u += "/"
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode}
	}
	var ms multiStatus
	if err := xml.Unmarshal(data, &ms); err != nil {
		return nil, err
	}
	return &ms, nil
}

func isTransient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}

func okStatus(s string) bool { return statusCode(s) == http.StatusOK }

func statusCode(s string) int {
	for _, part := range strings.Fields(s) {
		code, err := strconv.Atoi(part)
		if err == nil && code >= 100 && code < 600 {
			return code
		}
	}
	return 0
}

func normalizePath(h string) string {
	if h == "" {
		return ""
	}
	u, err := url.Parse(h)
	if err == nil && u.Path != "" {
		return u.Path
	}
	return h
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
