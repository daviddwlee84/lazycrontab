package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
)

func scheduleFields(expr string) []ui.Field {
	when := func(templates ...string) func(map[string]string) bool {
		return func(v map[string]string) bool {
			for _, s := range templates {
				if v["template"] == s {
					return true
				}
			}
			return false
		}
	}
	return []ui.Field{
		{Key: "template", Label: "Pattern (←/→ choose)", Value: "Custom cron", Options: []string{"Custom cron", "Every N minutes", "Hourly", "Daily", "Weekdays", "Weekly", "Monthly"}},
		{Key: "schedule", Label: "Cron expression", Value: expr, Show: when("Custom cron")},
		{Key: "interval", Label: "Interval in minutes", Value: "15", Show: when("Every N minutes")},
		{Key: "time", Label: "Time HH:MM", Value: "09:00", Show: when("Daily", "Weekdays", "Weekly", "Monthly")},
		{Key: "weekdays", Label: "Weekday list: 0=Sunday … 6=Saturday", Value: "1-5", Show: when("Weekly")},
		{Key: "monthday", Label: "Day of month", Value: "1", Show: when("Monthly")},
		{Key: "when", Label: "Optional English phrase", Advanced: true, Show: when("Custom cron")},
	}
}
func expression(v map[string]string) (string, error) {
	mode := v["template"]
	if mode == "" || mode == "Custom cron" {
		if v["when"] != "" {
			return schedule.FromHuman(v["when"])
		}
		return v["schedule"], nil
	}
	if mode == "Every N minutes" {
		return schedule.FromHuman("every " + v["interval"] + " minutes")
	}
	if mode == "Hourly" {
		return "0 * * * *", nil
	}
	t, err := time.Parse("15:04", v["time"])
	if err != nil {
		return "", fmt.Errorf("time must be HH:MM")
	}
	dom, dow := "*", "*"
	switch mode {
	case "Daily":
	case "Weekdays":
		dow = "1-5"
	case "Weekly":
		dow = v["weekdays"]
	case "Monthly":
		n, e := strconv.Atoi(v["monthday"])
		if e != nil || n < 1 || n > 31 {
			return "", fmt.Errorf("day of month must be 1..31")
		}
		dom = strconv.Itoa(n)
	default:
		return "", fmt.Errorf("unknown pattern")
	}
	return fmt.Sprintf("%d %d %s * %s", t.Minute(), t.Hour(), dom, dow), nil
}
func liveSchedule(dialect schedule.Dialect, loc *time.Location, locale string) func(map[string]string) string {
	return func(v map[string]string) string {
		expr, err := expression(v)
		if err != nil {
			return err.Error()
		}
		sc, err := schedule.Parse(expr, dialect, loc, locale)
		if err != nil {
			return err.Error()
		}
		next := sc.Next(time.Now().In(loc))
		n := "event trigger"
		if !next.IsZero() {
			n = next.Format("Mon 02 Jan 15:04 MST")
		}
		return expr + " → " + sc.Description + " · " + n
	}
}
func addSchedule(root *cobra.Command, o *options) {
	group := &cobra.Command{Use: "schedule", Short: "Validate, explain, preview and build cron expressions"}
	root.AddCommand(group)
	for _, op := range []string{"validate", "explain", "next"} {
		var dialect, timezone, locale, from string
		var count int
		cmd := &cobra.Command{Use: op + " EXPRESSION", Short: op + " a cron expression without accessing a crontab", Args: exactArgs(1)}
		cmd.Flags().StringVar(&dialect, "dialect", "system", "system or supercronic")
		cmd.Flags().StringVar(&timezone, "timezone", "Local", "IANA schedule timezone")
		cmd.Flags().StringVar(&locale, "locale", "en", "en or zh_TW")
		cmd.Flags().StringVar(&from, "from", "", "RFC3339 starting instant")
		cmd.Flags().IntVarP(&count, "count", "n", 5, "Next occurrences (1..1000)")
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			loc, e := time.LoadLocation(timezone)
			if e != nil {
				return usage("%v", e)
			}
			sc, e := schedule.Parse(args[0], schedule.Dialect(dialect), loc, locale)
			if e != nil {
				return usage("%v", e)
			}
			start := time.Now()
			if from != "" {
				start, e = time.Parse(time.RFC3339, from)
				if e != nil {
					return usage("%v", e)
				}
			}
			next, e := sc.NextN(cmd.Context(), start, count)
			if e != nil {
				return usage("%v", e)
			}
			data := map[string]any{"schedule": sc, "timezone": loc.String(), "next": next}
			lines := []string{sc.Expression, "  " + sc.Description}
			for _, t := range next {
				lines = append(lines, "  "+t.Format("Mon 2006-01-02 15:04:05 -07:00 MST"))
			}
			lines = append(lines, sc.Warnings...)
			if len(next) == 0 && !sc.Event {
				lines = append(lines, "No occurrence found within the parser search horizon.")
			}
			return o.emit(cmd, data, strings.Join(lines, "\n"))
		}
		group.AddCommand(cmd)
	}
	for _, name := range []string{"build", "playground"} {
		var when, expr, dialect, timezone string
		cmd := &cobra.Command{Use: name, Short: "Experiment with cron patterns and live explanations", Args: exactArgs(0)}
		cmd.Flags().StringVar(&when, "when", "", "Limited English schedule phrase")
		cmd.Flags().StringVar(&expr, "schedule", "", "Raw cron expression")
		cmd.Flags().StringVar(&dialect, "dialect", "system", "system or supercronic")
		cmd.Flags().StringVar(&timezone, "timezone", "Local", "IANA schedule timezone")
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			loc, e := time.LoadLocation(timezone)
			if e != nil {
				return usage("%v", e)
			}
			if when != "" && expr != "" {
				return usage("choose --when or --schedule")
			}
			if when != "" {
				expr, e = schedule.FromHuman(when)
				if e != nil {
					return usage("%v", e)
				}
			}
			if expr != "" {
				if _, e = schedule.Parse(expr, schedule.Dialect(dialect), loc, "en"); e != nil {
					return usage("%v", e)
				}
			}
			if !o.interactive && (expr != "" || o.json || !tty()) {
				if expr == "" {
					return usage("provide --when or --schedule, or use a terminal for the builder")
				}
				return o.emit(cmd, map[string]string{"expression": expr}, expr)
			}
			if expr == "" {
				expr = "0 9 * * 1-5"
			}
			fields := append(scheduleFields(expr), ui.Field{Key: "destination", Label: "After preview", Value: "Show expression", Options: []string{"Show expression", "Create job draft"}, Advanced: true})
			result, e := ui.RunForm(cmd.Context(), ui.FormSpec{Title: "Schedule playground · no crontab changes", Fields: fields, Mouse: true, Live: liveSchedule(schedule.Dialect(dialect), loc, "en"), Build: func(ctx context.Context, v map[string]string) (ui.Review, error) {
				x, e := expression(v)
				if e != nil {
					return ui.Review{}, e
				}
				sc, e := schedule.Parse(x, schedule.Dialect(dialect), loc, "en")
				if e != nil {
					return ui.Review{}, e
				}
				next, e := sc.NextN(ctx, time.Now().In(loc), 10)
				return ui.Review{Text: x + "\n" + sc.Description + "\n" + pretty(next) + "\n\nNext: " + v["destination"] + ". No job is saved by this preview.", Data: x}, e
			}, Apply: func(_ context.Context, v map[string]string, r ui.Review) (string, error) {
				if v["destination"] == "Create job draft" {
					return fmt.Sprint(r.Data) + "\nPress Enter to open the prefilled job wizard.", nil
				}
				return fmt.Sprint(r.Data), nil
			}})
			if e == nil && result.Values["destination"] == "Create job draft" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"add", "--interactive", "--schedule", fmt.Sprint(result.Review.Data)}
				if o.config != "" {
					args = append(args, "--config", o.config)
				}
				if o.host != "" {
					args = append(args, "--host", o.host)
				}
				if o.source != "" {
					args = append(args, "--source", o.source)
				}
				return transport.Interactive(exec.CommandContext(cmd.Context(), exe, args...))
			}
			return e
		}
		if name == "playground" {
			root.AddCommand(cmd)
		} else {
			group.AddCommand(cmd)
		}
	}
}
