package icalparse

import (
	"bytes"
	"strings"
	"time"

	"github.com/addvanced/icloud-calendar/internal/model"

	ical "github.com/emersion/go-ical"
)

func ParseEvent(raw string, loc *time.Location) (model.Event, error) {
	out := model.Event{ICalRaw: raw}
	cal, err := ical.NewDecoder(bytes.NewBufferString(raw)).Decode()
	if err != nil {
		return out, err
	}
	events := cal.Events()
	if len(events) == 0 {
		return out, nil
	}
	ev := events[0]
	out.UID, err = ev.Props.Text(ical.PropUID)
	if err != nil {
		return out, err
	}
	out.Summary = optionalText(ev, ical.PropSummary)
	out.Location = optionalText(ev, ical.PropLocation)
	out.Description = optionalText(ev, ical.PropDescription)
	out.AllDay = false
	if p := ev.Props.Get(ical.PropDateTimeStart); p != nil && p.ValueType() == ical.ValueDate {
		out.AllDay = true
	}
	out.StartTime, err = ev.DateTimeStart(loc)
	if err != nil {
		return out, err
	}
	out.EndTime, err = ev.DateTimeEnd(loc)
	if err != nil {
		if ev.Props.Get(ical.PropDateTimeEnd) != nil || ev.Props.Get(ical.PropDuration) != nil {
			return out, err
		}
		out.EndTime = defaultEndTime(out.StartTime, out.AllDay)
	} else if out.EndTime.IsZero() {
		out.EndTime = defaultEndTime(out.StartTime, out.AllDay)
	}
	out.LastModified, _ = ev.Props.DateTime(ical.PropLastModified, time.UTC)
	out.RecurrenceInfo = recurrenceInfo(ev)
	return out, nil
}

func defaultEndTime(start time.Time, allDay bool) time.Time {
	if allDay {
		return start.Add(24 * time.Hour)
	}
	return start
}

func optionalText(ev ical.Event, name string) string {
	s, _ := ev.Props.Text(name)
	return s
}

func recurrenceInfo(ev ical.Event) string {
	parts := []string{}
	if vals := ev.Props.Values(ical.PropRecurrenceRule); len(vals) > 0 {
		for _, v := range vals {
			parts = append(parts, "RRULE:"+v.Value)
		}
	}
	// Keep only recurrence markers for now. The raw iCalendar is persisted separately,
	// so full RDATE/EXDATE values remain available if recurrence expansion is added later.
	if vals := ev.Props.Values(ical.PropRecurrenceDates); len(vals) > 0 {
		parts = append(parts, "RDATE")
	}
	if vals := ev.Props.Values(ical.PropExceptionDates); len(vals) > 0 {
		parts = append(parts, "EXDATE")
	}
	return strings.Join(parts, ";")
}
