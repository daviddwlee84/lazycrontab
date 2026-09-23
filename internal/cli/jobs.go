package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/document"
	"github.com/daviddwlee84/lazycrontab/internal/schedule"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func addJobs(root *cobra.Command, o *options) {
	addCheck(root, o)
	var search string
	list := &cobra.Command{Use: "list", Short: "List jobs with human-readable schedules", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		snaps := s.List(cmd.Context(), o.hostID(c), o.sourceID(c))
		if len(snaps) == 0 {
			return usage("no matching host/source")
		}
		var lines []string
		failed := false
		for i := range snaps {
			snap := &snaps[i]
			if snap.Error != "" {
				failed = true
				lines = append(lines, snap.Host+"/"+snap.Source+": "+snap.Error)
				continue
			}
			filtered := []service.Entry{}
			for _, j := range snap.Entries {
				if search != "" && !strings.Contains(strings.ToLower(j.Name+" "+j.Command+" "+j.Remark), strings.ToLower(search)) {
					continue
				}
				filtered = append(filtered, j)
				state := "on"
				if !j.Enabled {
					state = "off"
				}
				next := "—"
				if len(j.Next) > 0 {
					next = j.Next[0].Format("01-02 15:04 MST")
				}
				lines = append(lines, fmt.Sprintf("%s/%s  %s  [%s] %s\n  %s · next %s\n  %s", j.Host, j.Source, j.ID, state, j.Name, j.Description, next, j.Command))
				if j.Diagnostic != "" {
					lines = append(lines, "  ! "+j.Diagnostic)
				}
			}
			snap.Entries = filtered
		}
		if len(lines) == 0 {
			lines = append(lines, "No jobs. Use lazycrontab add to create one.")
		}
		if e = o.emit(cmd, snaps, strings.Join(lines, "\n")); e != nil {
			return e
		}
		if failed {
			return fmt.Errorf("some sources could not be read")
		}
		return nil
	}}
	list.Flags().StringVar(&search, "search", "", "Filter name, command and remark")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "show ID", Short: "Inspect a job", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		_, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		for _, j := range snap.Entries {
			if j.ID == args[0] {
				return o.emit(cmd, j, pretty(j))
			}
		}
		return fmt.Errorf("job not found")
	}})
	root.AddCommand(&cobra.Command{Use: "export", Short: "Print the original crontab document", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		_, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		if o.json {
			return o.emit(cmd, map[string]string{"content": snap.Document.Raw}, "")
		}
		_, e = fmt.Fprint(cmd.OutOrStdout(), snap.Document.Raw)
		return e
	}})
	for _, op := range []string{"enable", "disable", "remove"} {
		cmd := &cobra.Command{Use: op + " ID", Short: op + " one job after review", Args: exactArgs(1)}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			s, snap, e := o.snapshot(cmd)
			if e != nil {
				return e
			}
			j, e := snap.Document.Find(args[0])
			if e != nil {
				return e
			}
			var replacement *document.Job
			if op != "remove" {
				j.Enabled = op == "enable"
				replacement = &j
			}
			p, e := s.Plan(cmd.Context(), snap, args[0], replacement, op)
			if e != nil {
				return e
			}
			return o.applyPlan(cmd, s, p)
		}
		root.AddCommand(cmd)
	}
	for _, op := range []string{"add", "edit"} {
		addJobCommand(root, o, op)
	}
}
func (o *options) snapshot(cmd *cobra.Command) (*service.Service, service.Snapshot, error) {
	c, e := o.load()
	if e != nil {
		return nil, service.Snapshot{}, e
	}
	s := service.New(c)
	snap, e := s.Snapshot(cmd.Context(), o.hostID(c), o.sourceID(c))
	return s, snap, e
}

type jobReview struct {
	Plan   service.Plan
	Recipe service.Recipe
	Entry  service.Entry
}

func addJobCommand(root *cobra.Command, o *options, op string) {
	cmd := &cobra.Command{Use: op, Short: op + " a job using flags or the shared wizard"}
	if op == "edit" {
		cmd.Use += " ID"
		cmd.Args = exactArgs(1)
	} else {
		cmd.Args = exactArgs(0)
	}
	f := cmd.Flags()
	for _, flag := range []struct{ name, value, help string }{
		{"name", "", "Short job name"}, {"schedule", "", "Cron expression"}, {"when", "", "Limited English schedule phrase"},
		{"command", "", "Complete shell command as one argument"}, {"remark", "", "Why this job exists"},
		{"runner", "direct", "direct or pueue"}, {"group", "", "Existing Pueue group"},
		{"directory", "", "Target working directory; script presets resolve relative paths"},
		{"output", "", "Append stdout and, by default, stderr to this file"}, {"stderr", "", "Append stderr separately"},
		{"script", "", "Target script path; metadata only with the command preset"}, {"log", "", "Explicit existing log path"},
		{"preset", "command", "command, executable, shell, python, uv-project, or uv-script"},
		{"runtime", "", "Target interpreter/uv executable (script presets)"}, {"project", "", "Target Python project directory (uv-project preset)"},
	} {
		f.String(flag.name, flag.value, flag.help)
	}
	f.Bool("disabled", false, "Save the job disabled")
	f.StringArray("arg", nil, "One exact script argument; repeat for more")
	f.StringArray("env", nil, "Literal per-job NAME=value; repeat for more")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		get := func(key string) string { value, _ := f.GetString(key); return value }
		if get("when") != "" && get("schedule") != "" {
			return usage("choose --when or --schedule")
		}
		if !service.ValidPreset(get("preset")) {
			return usage("unknown preset %q", get("preset"))
		}
		if get("runner") != "direct" && get("runner") != "pueue" {
			return usage("runner must be direct or pueue")
		}
		if f.Changed("command") && f.Changed("preset") && get("preset") != "command" {
			return usage("choose --command or a script --preset")
		}
		overrides := map[string]string{}
		for _, key := range []string{"name", "schedule", "command", "remark", "runner", "group", "directory", "output", "stderr", "script", "log", "preset", "runtime", "project"} {
			if f.Changed(key) {
				overrides[key] = get(key)
			}
		}
		if f.Changed("disabled") {
			disabled, _ := f.GetBool("disabled")
			overrides["enabled"] = fmt.Sprint(!disabled)
		}
		if get("when") != "" {
			expr, err := schedule.FromHuman(get("when"))
			if err != nil {
				return usage("%v", err)
			}
			overrides["schedule"] = expr
		}
		if f.Changed("arg") {
			argv, _ := f.GetStringArray("arg")
			overrides["args"] = transport.Join(argv)
		}
		if f.Changed("env") {
			env, _ := f.GetStringArray("env")
			if _, err := service.ParseEnvironment(env); err != nil {
				return usage("%v", err)
			}
			overrides["environment"] = transport.Join(env)
		}
		business := false
		f.Visit(func(flag *pflag.Flag) {
			if root.PersistentFlags().Lookup(flag.Name) == nil {
				business = true
			}
		})
		wizard := o.interactive || (!business && tty() && !o.json && !o.dry)
		if op == "add" && !wizard && overrides["schedule"] == "" {
			return usage("--schedule (or --when) is required; use add --interactive for the wizard")
		}
		cfg, err := o.load()
		if err != nil {
			return err
		}
		s := service.New(cfg)
		host, source := o.hostID(cfg), o.sourceID(cfg)
		src, err := cfg.Source(host, source)
		if err != nil {
			return err
		}
		if expr := overrides["schedule"]; expr != "" {
			if _, err := schedule.Parse(expr, sourceDialect(src), time.UTC, cfg.Locale); err != nil {
				return usage("%v", err)
			}
		}
		id := ""
		if op == "edit" {
			id = args[0]
		}
		spec, values, err := newJobFormSpec(cmd.Context(), s, host, source, op, id, overrides)
		if err != nil {
			return err
		}
		if values["preset"] == "command" && (f.Changed("runtime") || f.Changed("project") || f.Changed("arg")) {
			return usage("--runtime, --project and --arg require a script preset")
		}
		if wizard {
			_, err = ui.RunForm(cmd.Context(), spec)
			return err
		}
		if values["preset"] == "command" && values["command"] == "" {
			return usage("--command is required, or choose --preset and --script; use --interactive for the wizard")
		}
		review, err := spec.Build(cmd.Context(), values)
		if err != nil {
			return err
		}
		return o.approve(cmd, op+" job", review.Data.(jobReview).Plan, review.Text, func(ctx context.Context) (any, string, error) {
			message, err := spec.Apply(ctx, values, review)
			return map[string]any{"job_id": review.Data.(jobReview).Plan.JobID, "result": message}, message, err
		})
	}
	root.AddCommand(cmd)
}
