package model

import "time"

type Calendar struct {
	URL        string
	Name       string
	SyncToken  string
	LastSyncAt time.Time
	ETag       string
}

type Event struct {
	UID            string
	Href           string
	CalendarURL    string
	CalendarName   string
	ETag           string
	ICalRaw        string
	Summary        string
	Location       string
	Description    string
	StartTime      time.Time
	EndTime        time.Time
	AllDay         bool
	LastModified   time.Time
	RecurrenceInfo string
	SyncedAt       time.Time
}

type SyncChange struct {
	Href    string
	ETag    string
	Deleted bool
}
