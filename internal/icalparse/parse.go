package icalparse

import (
	"bytes"
	"regexp"
	"strconv"
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
	out.StartTime, err = dateTime(ev.Props.Get(ical.PropDateTimeStart), loc)
	if err != nil {
		return out, err
	}
	if endProp := ev.Props.Get(ical.PropDateTimeEnd); endProp != nil {
		out.EndTime, err = dateTime(endProp, loc)
		if err != nil {
			return out, err
		}
	} else if durationProp := ev.Props.Get(ical.PropDuration); durationProp != nil {
		duration, err := durationProp.Duration()
		if err != nil {
			return out, err
		}
		out.EndTime = out.StartTime.Add(duration)
	} else {
		out.EndTime = defaultEndTime(out.StartTime, out.AllDay)
	}
	out.LastModified, _ = ev.Props.DateTime(ical.PropLastModified, time.UTC)
	out.RecurrenceInfo = recurrenceInfo(ev)
	return out, nil
}

var gmtOffsetTimezone = regexp.MustCompile(`^GMT([+-])(\d{2})(?::?(\d{2}))?$`)

// dateTime parses an iCalendar date-time while accepting iCloud's fixed-offset
// TZID values such as GMT-0500, which are not IANA location names.
func dateTime(prop *ical.Prop, fallback *time.Location) (time.Time, error) {
	if prop == nil {
		return time.Time{}, nil
	}
	if tzid := prop.Params.Get(ical.PropTimezoneID); tzid != "" {
		if loc, ok := fixedOffsetLocation(tzid); ok {
			// go-ical always resolves TZID itself. Fixed-offset TZIDs are not
			// in Go's IANA zone database, so temporarily omit it while parsing.
			prop.Params.Del(ical.PropTimezoneID)
			defer prop.Params.Set(ical.PropTimezoneID, tzid)
			return prop.DateTime(loc)
		}
	}
	return prop.DateTime(fallback)
}

func fixedOffsetLocation(tzid string) (*time.Location, bool) {
	matches := gmtOffsetTimezone.FindStringSubmatch(tzid)
	if matches == nil {
		return nil, false
	}
	hours, _ := strconv.Atoi(matches[2])
	minutes := 0
	if matches[3] != "" {
		minutes, _ = strconv.Atoi(matches[3])
	}
	if hours > 23 || minutes > 59 {
		return nil, false
	}
	offset := hours*60*60 + minutes*60
	if matches[1] == "-" {
		offset = -offset
	}
	return time.FixedZone(tzid, offset), true
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
