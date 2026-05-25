package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/addvanced/icloud-calendar/internal/model"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "icalendar", "events.db"), nil
}

func Open(path string) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// #nosec G304 -- path is either the fixed default cache path or an explicit test path.
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("database permissions must be 600 or stricter, got %03o", st.Mode().Perm())
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) DB() *sql.DB  { return s.db }

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS calendars (
  url TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  sync_token TEXT,
  last_sync_at TIMESTAMP,
  etag TEXT
);
CREATE TABLE IF NOT EXISTS app_locks (
  name TEXT PRIMARY KEY,
  locked_at TIMESTAMP NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  uid TEXT NOT NULL,
  href TEXT NOT NULL,
  calendar_url TEXT NOT NULL,
  etag TEXT NOT NULL,
  ical_raw TEXT NOT NULL,
  summary TEXT,
  location TEXT,
  description TEXT,
  start_time TIMESTAMP,
  end_time TIMESTAMP,
  all_day BOOLEAN,
  last_modified TIMESTAMP,
  recurrence_info TEXT,
  synced_at TIMESTAMP,
  PRIMARY KEY (calendar_url, href),
  FOREIGN KEY (calendar_url) REFERENCES calendars(url) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_events_time ON events(start_time, end_time);
CREATE INDEX IF NOT EXISTS idx_events_calendar ON events(calendar_url);
CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(summary, location, description, content='events', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS events_ai AFTER INSERT ON events BEGIN
  INSERT INTO events_fts(rowid, summary, location, description) VALUES (new.rowid, new.summary, new.location, new.description);
END;
CREATE TRIGGER IF NOT EXISTS events_ad AFTER DELETE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, summary, location, description) VALUES('delete', old.rowid, old.summary, old.location, old.description);
END;
CREATE TRIGGER IF NOT EXISTS events_au AFTER UPDATE ON events BEGIN
  INSERT INTO events_fts(events_fts, rowid, summary, location, description) VALUES('delete', old.rowid, old.summary, old.location, old.description);
  INSERT INTO events_fts(rowid, summary, location, description) VALUES (new.rowid, new.summary, new.location, new.description);
END;`

func (s *Store) AcquireLock(ctx context.Context, name string) (func(context.Context) error, error) {
	// Personal-calendar syncs should finish well below 30 minutes. This stale-lock cleanup
	// intentionally favours simplicity over heartbeat/lease renewal; a legitimately longer
	// sync could be considered stale and allow a second sync process to start.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM app_locks WHERE name=? AND locked_at < ?`, name, time.Now().Add(-30*time.Minute).Round(0)); err != nil {
		log.Printf("warning: stale lock cleanup failed: %v", err)
	}
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO app_locks(name, locked_at) VALUES(?, ?)`, name, time.Now().Round(0))
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%s is already running", name)
	}
	return func(ctx context.Context) error {
		_, err := s.db.ExecContext(ctx, `DELETE FROM app_locks WHERE name=?`, name)
		return err
	}, nil
}

func (s *Store) UpsertCalendar(ctx context.Context, c model.Calendar) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO calendars(url,name,sync_token,last_sync_at,etag) VALUES(?,?,?,?,?)
ON CONFLICT(url) DO UPDATE SET name=excluded.name, sync_token=excluded.sync_token, last_sync_at=excluded.last_sync_at, etag=excluded.etag`, c.URL, c.Name, c.SyncToken, nullableTime(c.LastSyncAt), c.ETag)
	return err
}

func (s *Store) CalendarByURL(ctx context.Context, url string) (model.Calendar, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT url,name,coalesce(sync_token,''),last_sync_at,coalesce(etag,'') FROM calendars WHERE url=?`, url)
	var c model.Calendar
	var last sql.NullTime
	if err := row.Scan(&c.URL, &c.Name, &c.SyncToken, &last, &c.ETag); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Calendar{}, false, nil
		}
		return model.Calendar{}, false, err
	}
	if last.Valid {
		c.LastSyncAt = last.Time
	}
	return c, true, nil
}

func (s *Store) Calendars(ctx context.Context) ([]model.Calendar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url,name,coalesce(sync_token,''),last_sync_at,coalesce(etag,'') FROM calendars ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Calendar
	for rows.Next() {
		var c model.Calendar
		var last sql.NullTime
		if err := rows.Scan(&c.URL, &c.Name, &c.SyncToken, &last, &c.ETag); err != nil {
			return nil, err
		}
		if last.Valid {
			c.LastSyncAt = last.Time
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpsertEvent(ctx context.Context, e model.Event) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(uid,href,calendar_url,etag,ical_raw,summary,location,description,start_time,end_time,all_day,last_modified,recurrence_info,synced_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(calendar_url,href) DO UPDATE SET uid=excluded.uid, etag=excluded.etag, ical_raw=excluded.ical_raw, summary=excluded.summary, location=excluded.location, description=excluded.description, start_time=excluded.start_time, end_time=excluded.end_time, all_day=excluded.all_day, last_modified=excluded.last_modified, recurrence_info=excluded.recurrence_info, synced_at=excluded.synced_at`, e.UID, e.Href, e.CalendarURL, e.ETag, e.ICalRaw, e.Summary, e.Location, e.Description, e.StartTime, e.EndTime, e.AllDay, nullableTime(e.LastModified), e.RecurrenceInfo, nullableTime(e.SyncedAt))
	return err
}

func (s *Store) DeleteEvent(ctx context.Context, calendarURL, href string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE calendar_url=? AND href=?`, calendarURL, href)
	return err
}

func (s *Store) EventsInRange(ctx context.Context, from, to time.Time, calendarName string) ([]model.Event, error) {
	q := `SELECT e.uid,e.href,e.calendar_url,c.name,e.etag,e.ical_raw,coalesce(e.summary,''),coalesce(e.location,''),coalesce(e.description,''),e.start_time,e.end_time,e.all_day,e.last_modified,coalesce(e.recurrence_info,''),e.synced_at FROM events e JOIN calendars c ON c.url=e.calendar_url WHERE e.end_time > ? AND e.start_time < ?`
	args := []any{from, to}
	if calendarName != "" {
		q += ` AND c.name COLLATE NOCASE = ?`
		args = append(args, calendarName)
	}
	q += ` ORDER BY e.start_time, e.summary`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (s *Store) Search(ctx context.Context, query, field string) ([]model.Event, error) {
	match := query
	if field != "" {
		switch field {
		case "title":
			field = "summary"
		case "location", "description", "summary":
		default:
			return nil, fmt.Errorf("unsupported search field: %s", field)
		}
		match = field + ":" + query
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.uid,e.href,e.calendar_url,c.name,e.etag,e.ical_raw,coalesce(e.summary,''),coalesce(e.location,''),coalesce(e.description,''),e.start_time,e.end_time,e.all_day,e.last_modified,coalesce(e.recurrence_info,''),e.synced_at FROM events_fts f JOIN events e ON e.rowid=f.rowid JOIN calendars c ON c.url=e.calendar_url WHERE events_fts MATCH ? ORDER BY e.start_time`, match)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]model.Event, error) {
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var lm, sa sql.NullTime
		if err := rows.Scan(&e.UID, &e.Href, &e.CalendarURL, &e.CalendarName, &e.ETag, &e.ICalRaw, &e.Summary, &e.Location, &e.Description, &e.StartTime, &e.EndTime, &e.AllDay, &lm, &e.RecurrenceInfo, &sa); err != nil {
			return nil, err
		}
		if lm.Valid {
			e.LastModified = lm.Time
		}
		if sa.Valid {
			e.SyncedAt = sa.Time
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) EventByID(ctx context.Context, id string) (model.Event, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.uid,e.href,e.calendar_url,c.name,e.etag,e.ical_raw,coalesce(e.summary,''),coalesce(e.location,''),coalesce(e.description,''),e.start_time,e.end_time,e.all_day,e.last_modified,coalesce(e.recurrence_info,''),e.synced_at FROM events e JOIN calendars c ON c.url=e.calendar_url WHERE e.uid=? OR e.href=? ORDER BY e.start_time DESC LIMIT 1`, id, id)
	if err != nil {
		return model.Event{}, false, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return model.Event{}, false, err
	}
	if len(events) == 0 {
		return model.Event{}, false, nil
	}
	return events[0], true, nil
}

func (s *Store) Status(ctx context.Context) (calendarCount int, eventCount int, lastSync time.Time, err error) {
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM calendars`).Scan(&calendarCount); err != nil {
		return
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM events`).Scan(&eventCount); err != nil {
		return
	}
	var last sql.NullTime
	if err = s.db.QueryRowContext(ctx, `SELECT last_sync_at FROM calendars WHERE last_sync_at IS NOT NULL ORDER BY last_sync_at DESC LIMIT 1`).Scan(&last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
		return
	}
	if last.Valid {
		lastSync = last.Time
	}
	return
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Round(0)
}
