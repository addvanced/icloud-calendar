package syncengine

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/addvanced/icloud-calendar/internal/caldav"
	"github.com/addvanced/icloud-calendar/internal/db"
	"github.com/addvanced/icloud-calendar/internal/icalparse"
	"github.com/addvanced/icloud-calendar/internal/model"
)

type CalDAV interface {
	Discover(context.Context) ([]model.Calendar, error)
	QueryObjects(context.Context, string, time.Time, time.Time) ([]caldav.Object, error)
	SyncCollection(context.Context, string, string) ([]model.SyncChange, string, error)
	MultiGet(context.Context, string, []string) ([]caldav.Object, error)
}

type Engine struct {
	Store      *db.Store
	Client     CalDAV
	Location   *time.Location
	RangeYears int
	Now        func() time.Time
}

type Result struct {
	Calendars int
	Upserted  int
	Deleted   int
	Full      bool
}

func (e *Engine) Sync(ctx context.Context, full bool) (Result, error) {
	if e.Store == nil || e.Client == nil {
		return Result{}, errors.New("store and client are required")
	}
	location := e.Location
	if location == nil {
		location = time.UTC
	}
	rangeYears := e.RangeYears
	if rangeYears == 0 {
		rangeYears = 2
	}

	release, err := e.Store.AcquireLock(ctx, "sync")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = release(context.Background()) }()

	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	from, to := now.AddDate(-rangeYears, 0, 0), now.AddDate(rangeYears, 0, 0)
	calendars, err := e.Client.Discover(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Calendars: len(calendars), Full: full}
	for _, discovered := range calendars {
		cal := discovered
		stored, exists, err := e.Store.CalendarByURL(ctx, cal.URL)
		if err != nil {
			return res, err
		}
		storedToken := ""
		if exists {
			storedToken = stored.SyncToken
		}
		// Persist the calendar before events so new calendars satisfy the events.calendar_url
		// foreign key. A second upsert after successful sync records token/LastSyncAt.
		if err := e.Store.UpsertCalendar(ctx, cal); err != nil {
			return res, err
		}
		if full || storedToken == "" {
			objects, err := e.Client.QueryObjects(ctx, cal.URL, from, to)
			if err != nil {
				return res, err
			}
			for _, obj := range objects {
				if err := e.saveObject(ctx, cal, obj, now, location); err != nil {
					return res, err
				}
				res.Upserted++
			}
			cal.LastSyncAt = now
			if err := e.Store.UpsertCalendar(ctx, cal); err != nil {
				return res, err
			}
			continue
		}
		changes, nextToken, err := e.Client.SyncCollection(ctx, cal.URL, storedToken)
		if err != nil {
			if caldav.IsInvalidSyncToken(err) {
				objects, qerr := e.Client.QueryObjects(ctx, cal.URL, from, to)
				if qerr != nil {
					return res, qerr
				}
				for _, obj := range objects {
					if err := e.saveObject(ctx, cal, obj, now, location); err != nil {
						return res, err
					}
					res.Upserted++
				}
				cal.LastSyncAt = now
				if err := e.Store.UpsertCalendar(ctx, cal); err != nil {
					return res, err
				}
				continue
			}
			return res, err
		}
		var hrefs []string
		for _, ch := range changes {
			if ch.Deleted {
				if err := e.Store.DeleteEvent(ctx, cal.URL, ch.Href); err != nil {
					return res, err
				}
				res.Deleted++
			} else {
				hrefs = append(hrefs, ch.Href)
			}
		}
		if len(hrefs) > 0 {
			objects, err := e.Client.MultiGet(ctx, cal.URL, hrefs)
			if err != nil {
				return res, err
			}
			for _, obj := range objects {
				if err := e.saveObject(ctx, cal, obj, now, location); err != nil {
					return res, err
				}
				res.Upserted++
			}
		}
		if nextToken != "" {
			cal.SyncToken = nextToken
		}
		cal.LastSyncAt = now
		if err := e.Store.UpsertCalendar(ctx, cal); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (e *Engine) saveObject(ctx context.Context, cal model.Calendar, obj caldav.Object, syncedAt time.Time, location *time.Location) error {
	ev, err := icalparse.ParseEvent(obj.Raw, location)
	if err != nil {
		return err
	}
	if ev.UID == "" {
		log.Printf("warning: skipping event without UID at %s", obj.Href)
		return nil
	}
	ev.Href = obj.Href
	ev.CalendarURL = cal.URL
	ev.CalendarName = cal.Name
	ev.ETag = obj.ETag
	ev.SyncedAt = syncedAt
	return e.Store.UpsertEvent(ctx, ev)
}
