package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/addvanced/icloud-calendar/internal/caldav"
	"github.com/addvanced/icloud-calendar/internal/config"
	"github.com/addvanced/icloud-calendar/internal/db"
	"github.com/addvanced/icloud-calendar/internal/model"
	"github.com/addvanced/icloud-calendar/internal/syncengine"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type app struct {
	jsonOut  bool
	calendar string
	noSync   bool
	cfg      config.Config
	store    *db.Store
	loc      *time.Location
}

func main() {
	a := &app{}
	root := &cobra.Command{Use: "icalendar", Short: "Read-only iCloud Calendar CLI"}
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", false, "machine-readable JSON output")
	root.PersistentFlags().StringVar(&a.calendar, "calendar", "", "filter by calendar name")
	root.PersistentFlags().BoolVar(&a.noSync, "no-sync", false, "skip stale-cache auto sync")
	root.AddCommand(a.setupCmd(), a.syncCmd(), a.statusCmd(), a.listCalendarsCmd(), a.todayCmd(), a.tomorrowCmd(), a.weekCmd(), a.nextWeekCmd(), a.rangeCmd(), a.searchCmd(), a.showCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (a *app) open() error {
	var err error
	if a.cfg, err = config.Load(""); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if a.store, err = db.Open(""); err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if a.loc, err = time.LoadLocation(a.cfg.Output.Timezone); err != nil {
		return fmt.Errorf("load timezone %q: %w", a.cfg.Output.Timezone, err)
	}
	return nil
}

func (a *app) close() {
	if a.store != nil {
		_ = a.store.Close()
	}
}

func (a *app) engine() *syncengine.Engine {
	return &syncengine.Engine{
		Store:      a.store,
		Client:     caldav.New(a.cfg.Auth.AppleID, a.cfg.Auth.AppPassword),
		Location:   a.loc,
		RangeYears: a.cfg.Sync.RangeYears,
	}
}

func (a *app) ensureFresh(ctx context.Context) error {
	if a.noSync {
		return nil
	}
	_, _, last, err := a.store.Status(ctx)
	if err != nil {
		return fmt.Errorf("read cache status: %w", err)
	}
	if last.IsZero() || time.Since(last) > time.Duration(a.cfg.Sync.AutoSyncThresholdMinutes)*time.Minute {
		if _, err := a.engine().Sync(ctx, false); err != nil {
			return fmt.Errorf("auto-sync stale cache: %w", err)
		}
	}
	return nil
}

func (a *app) setupCmd() *cobra.Command {
	return &cobra.Command{Use: "setup", RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Apple ID: ")
		appleID, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read Apple ID: %w", err)
		}
		appleID = strings.TrimSpace(appleID)
		fmt.Println("Generate an app-specific password at https://appleid.apple.com/ → Sign-In and Security → App-Specific Passwords.")
		fmt.Print("App-specific password: ")
		pw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read app-specific password: %w", err)
		}

		defaultTZ := config.DetectSystemTimezone()
		fmt.Printf("Timezone [%s]: ", defaultTZ)
		tz, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read timezone: %w", err)
		}
		tz = strings.TrimSpace(tz)
		if tz == "" {
			tz = defaultTZ
		} else if _, err := time.LoadLocation(tz); err != nil {
			fmt.Printf("warning: %q is not a valid IANA timezone; using %s instead\n", tz, defaultTZ)
			tz = defaultTZ
		}

		fmt.Print("Date format (Go time layout) [2006-01-02 15:04]: ")
		df, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read date format: %w", err)
		}
		df = strings.TrimSpace(df)
		if df == "" {
			df = "2006-01-02 15:04"
		}

		cfg := config.Config{
			Auth:   config.AuthConfig{AppleID: appleID, AppPassword: strings.TrimSpace(string(pw))},
			Sync:   config.SyncConfig{RangeYears: 2, AutoSyncThresholdMinutes: 15},
			Output: config.OutputConfig{DateFormat: df, Timezone: tz},
		}
		client := caldav.New(cfg.Auth.AppleID, cfg.Auth.AppPassword)
		ctx, cancel := context.WithTimeout(cmd.Context(), 45*time.Second)
		defer cancel()
		calendars, err := client.Discover(ctx)
		if err != nil {
			return fmt.Errorf("iCloud connection failed: %w", err)
		}
		if err := config.Save("", cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Config saved. Found %d calendars. Run: icalendar sync\n", len(calendars))
		return nil
	}}
}

func (a *app) syncCmd() *cobra.Command {
	var full, quiet bool
	cmd := &cobra.Command{Use: "sync", RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		res, err := a.engine().Sync(ctx, full)
		if err != nil {
			return fmt.Errorf("sync calendars: %w", err)
		}
		if quiet {
			return nil
		}
		if a.jsonOut {
			return json.NewEncoder(os.Stdout).Encode(res)
		}
		mode := "incremental"
		if full {
			mode = "full"
		}
		fmt.Printf("%s sync: calendars=%d upserted=%d deleted=%d\n", mode, res.Calendars, res.Upserted, res.Deleted)
		return nil
	}}
	cmd.Flags().BoolVar(&full, "full", false, "force full re-sync")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "silent unless errors")
	return cmd
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		cc, ec, last, err := a.store.Status(ctx)
		if err != nil {
			return fmt.Errorf("read status: %w", err)
		}
		out := map[string]any{"calendars": cc, "events": ec, "last_sync_at": timeOrNil(last)}
		if a.jsonOut {
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		fmt.Printf("Calendars: %d\nEvents: %d\nLast sync: %s\n", cc, ec, formatTime(last, a.loc))
		return nil
	}}
}

func (a *app) listCalendarsCmd() *cobra.Command {
	return &cobra.Command{Use: "list-calendars", RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		if err := a.ensureFresh(ctx); err != nil {
			return err
		}
		cals, err := a.store.Calendars(ctx)
		if err != nil {
			return fmt.Errorf("list calendars: %w", err)
		}
		if a.jsonOut {
			return json.NewEncoder(os.Stdout).Encode(cals)
		}
		for _, c := range cals {
			fmt.Printf("%s\n", c.Name)
		}
		return nil
	}}
}

func (a *app) todayCmd() *cobra.Command    { return rangeShortcut("today", 0, 0, a) }
func (a *app) tomorrowCmd() *cobra.Command { return rangeShortcut("tomorrow", 1, 1, a) }
func (a *app) weekCmd() *cobra.Command     { return rangeShortcut("week", 0, 6, a) }
func (a *app) nextWeekCmd() *cobra.Command { return rangeShortcut("next-week", 7, 13, a) }
func rangeShortcut(name string, startOffset, endOffset int, a *app) *cobra.Command {
	return &cobra.Command{Use: name, RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		now := time.Now().In(a.loc)
		from := dayStart(now.AddDate(0, 0, startOffset), a.loc)
		to := dayStart(now.AddDate(0, 0, endOffset+1), a.loc)
		return a.printRangeOpen(cmd.Context(), from, to)
	}}
}

func (a *app) rangeCmd() *cobra.Command {
	var fromS, toS string
	cmd := &cobra.Command{Use: "range --from <date> --to <date>", RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		from, err := parseDate(fromS, a.loc)
		if err != nil {
			return fmt.Errorf("parse --from date %q: %w", fromS, err)
		}
		to, err := parseDate(toS, a.loc)
		if err != nil {
			return fmt.Errorf("parse --to date %q: %w", toS, err)
		}
		to = to.AddDate(0, 0, 1)
		return a.printRangeOpen(cmd.Context(), from, to)
	}}
	cmd.Flags().StringVar(&fromS, "from", "", "YYYY-MM-DD")
	cmd.Flags().StringVar(&toS, "to", "", "YYYY-MM-DD")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func (a *app) searchCmd() *cobra.Command {
	var field string
	cmd := &cobra.Command{Use: "search <keyword>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		if err := a.ensureFresh(ctx); err != nil {
			return err
		}
		events, err := a.store.Search(ctx, args[0], field)
		if err != nil {
			return fmt.Errorf("search events: %w", err)
		}
		events = filterCalendar(events, a.calendar)
		return a.printEvents(events)
	}}
	cmd.Flags().StringVar(&field, "field", "", "title|location|description")
	return cmd
}

func (a *app) showCmd() *cobra.Command {
	return &cobra.Command{Use: "show <event-id-or-href>", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if err := a.open(); err != nil {
			return err
		}
		defer a.close()
		if err := a.ensureFresh(ctx); err != nil {
			return err
		}
		ev, ok, err := a.store.EventByID(ctx, args[0])
		if err != nil {
			return fmt.Errorf("load event %q: %w", args[0], err)
		}
		if !ok {
			return fmt.Errorf("event not found: %s", args[0])
		}
		if a.jsonOut {
			return json.NewEncoder(os.Stdout).Encode(toJSON(ev))
		}
		printEventDetail(ev, a.loc)
		return nil
	}}
}

func (a *app) printRangeOpen(ctx context.Context, from, to time.Time) error {
	if err := a.ensureFresh(ctx); err != nil {
		return err
	}
	events, err := a.store.EventsInRange(ctx, from, to, a.calendar)
	if err != nil {
		return fmt.Errorf("query events in range: %w", err)
	}
	return a.printEvents(events)
}

func (a *app) printEvents(events []model.Event) error {
	if a.jsonOut {
		arr := make([]eventJSON, 0, len(events))
		for _, e := range events {
			arr = append(arr, toJSON(e))
		}
		return json.NewEncoder(os.Stdout).Encode(arr)
	}
	printGrouped(events, a.loc)
	return nil
}

type eventJSON struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Start          string `json:"start"`
	End            string `json:"end"`
	AllDay         bool   `json:"all_day"`
	Location       string `json:"location"`
	Description    string `json:"description"`
	CalendarName   string `json:"calendar_name"`
	RecurrenceInfo string `json:"recurrence_info"`
	LastModified   string `json:"last_modified"`
}

func toJSON(e model.Event) eventJSON {
	return eventJSON{
		ID:             e.UID,
		Title:          e.Summary,
		Start:          e.StartTime.Format(time.RFC3339),
		End:            e.EndTime.Format(time.RFC3339),
		AllDay:         e.AllDay,
		Location:       e.Location,
		Description:    e.Description,
		CalendarName:   e.CalendarName,
		RecurrenceInfo: e.RecurrenceInfo,
		LastModified:   timeString(e.LastModified),
	}
}

func printGrouped(events []model.Event, loc *time.Location) {
	if len(events) == 0 {
		fmt.Println("No events.")
		return
	}
	cur := ""
	for _, e := range events {
		day := e.StartTime.In(loc).Format("Monday 02/01")
		if day != cur {
			if cur != "" {
				fmt.Println()
			}
			fmt.Println(day)
			cur = day
		}
		prefix := e.StartTime.In(loc).Format("02/01 15:04") + "-" + e.EndTime.In(loc).Format("15:04")
		if e.AllDay {
			prefix = "All-day"
		}
		locText := ""
		if e.Location != "" {
			locText = " @ " + e.Location
		}
		fmt.Printf("  %s  %s%s [%s] id:%s\n", prefix, e.Summary, locText, e.CalendarName, e.UID)
	}
}

func printEventDetail(e model.Event, loc *time.Location) {
	fmt.Printf("Title: %s\nID: %s\nCalendar: %s\nStart: %s\nEnd: %s\nAll-day: %t\n", e.Summary, e.UID, e.CalendarName, formatTime(e.StartTime, loc), formatTime(e.EndTime, loc), e.AllDay)
	if e.Location != "" {
		fmt.Println("Location:", e.Location)
	}
	if e.Description != "" {
		fmt.Println("Description:", e.Description)
	}
	if e.RecurrenceInfo != "" {
		fmt.Println("Recurrence:", e.RecurrenceInfo)
	}
}

func parseDate(s string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, loc)
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	tt := t.In(loc)
	return time.Date(tt.Year(), tt.Month(), tt.Day(), 0, 0, 0, 0, loc)
}

func filterCalendar(events []model.Event, cal string) []model.Event {
	if cal == "" {
		return events
	}
	out := []model.Event{}
	for _, e := range events {
		if e.CalendarName == cal {
			out = append(out, e)
		}
	}
	return out
}

func formatTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "never"
	}
	return t.In(loc).Format("02/01 15:04")
}

func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func timeOrNil(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}
func init() { cobra.EnableCommandSorting = false }
