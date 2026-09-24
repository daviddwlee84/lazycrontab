package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
)

func entry(snap service.Snapshot, id string) (service.Entry, error) {
	for _, e := range snap.Entries {
		if e.ID == id {
			return e, nil
		}
	}
	return service.Entry{}, fmt.Errorf("job %q not found", id)
}

const pueueOutputHint = "Pueue captures stdout/stderr. View: pueue log or lazypueue."

func pueueFields(ctx context.Context, s *service.Service, host string) []ui.FieldUpdate {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	caps := s.Capabilities(ctx, host)
	disabled := map[string]string{}
	if !caps.Ready {
		disabled["pueue"] = caps.Error
	}
	out := []ui.FieldUpdate{{Key: "runner", Options: []string{"direct", "pueue"}, Unavailable: disabled}}
	if caps.Ready {
		out = append(out, ui.FieldUpdate{Key: "group", Options: append([]string{""}, caps.Groups...), Hint: pueueOutputHint})
	}
	return out
}
func addExecution(root *cobra.Command, o *options) {
	var runner, group string
	run := &cobra.Command{Use: "run ID", Short: "Review and execute one job on its owning host", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if runner != "" && runner != "direct" && runner != "pueue" {
			return usage("runner must be direct or pueue")
		}
		s, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		j, e := entry(snap, args[0])
		if e != nil {
			return e
		}
		recipe, e := service.LoadRecipe(j)
		if e != nil {
			return e
		}
		if recipe.Runner == "" {
			recipe.Runner = "direct"
		}
		if cmd.Flags().Changed("runner") {
			recipe.Runner = runner
		}
		if cmd.Flags().Changed("group") {
			recipe.Group = group
		}
		spec := runFormSpec(s, snap, j, recipe, cmd.Flags().Changed("runner") || cmd.Flags().Changed("group"))
		build := spec.Build
		values := map[string]string{"runner": recipe.Runner, "group": recipe.Group}
		if o.interactive {
			_, e = ui.RunForm(cmd.Context(), spec)
			return e
		}

		r, e := build(cmd.Context(), values)
		if e != nil {
			return e
		}
		return o.approve(cmd, "Run · "+j.Key(), r.Data, r.Text, func(ctx context.Context) (any, string, error) {
			record, e := s.Run(ctx, r.Data.(service.ExecutionPlan))
			if e != nil && record.ExitCode > 0 && record.ExitCode < 126 {
				e = exitError{record.ExitCode, e}
			}
			return record, pretty(record), e
		})
	}}
	run.Flags().StringVar(&runner, "runner", "", "Override direct or pueue")
	run.Flags().StringVar(&group, "group", "", "Override existing Pueue group")
	root.AddCommand(run)
	var logPath string
	var lines int
	logs := &cobra.Command{Use: "logs ID", Short: "Read explicit logs or recorded manual-run output", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		j, e := entry(snap, args[0])
		if e != nil {
			return e
		}
		content, e := s.Logs(cmd.Context(), j, logPath, lines)
		if e != nil {
			return e
		}
		return o.emit(cmd, map[string]string{"host": j.Host, "source": j.Source, "job_id": j.ID, "output": content}, content)
	}}
	logs.Flags().StringVar(&logPath, "path", "", "Explicit target-side log path")
	logs.Flags().IntVar(&lines, "lines", 200, "Tail lines (1..10000)")
	root.AddCommand(logs)
	scripts := &cobra.Command{Use: "script", Short: "Edit existing or managed job scripts"}
	root.AddCommand(scripts)
	var scriptPath string
	edit := &cobra.Command{Use: "edit ID", Short: "Edit a private copy, then compare and save to the target", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !o.dry && (o.json || !tty()) {
			return usage("script edit requires a terminal")
		}
		s, snap, e := o.snapshot(cmd)
		if e != nil {
			return e
		}
		j, e := entry(snap, args[0])
		if e != nil {
			return e
		}
		if j.ReadOnly {
			return fmt.Errorf("source is read-only")
		}
		path := scriptPath
		r, recipeErr := service.LoadRecipe(j)
		if path == "" {
			if recipeErr != nil {
				return recipeErr
			}
			path = r.Script
		}
		if path == "" {
			return usage("set --path or store a --script path with edit ID")
		}
		if o.dry {
			return o.emit(cmd, map[string]string{"host": j.Host, "path": path}, j.Host+":"+path)
		}
		if recipeErr == nil && r.ManagedScript != nil && filepath.Clean(path) == filepath.Clean(r.Script) {
			return o.editManagedScript(cmd, s, j, r)
		}
		if service.IsManagedScriptPath(path) {
			return fmt.Errorf("managed script versions are immutable; edit the owning job with matching helper metadata")
		}
		draft, e := s.ScriptDraft(cmd.Context(), j.Host, path)
		if e != nil {
			return e
		}
		defer os.Remove(draft.File)
		if e = transport.Interactive(editor(draft.File)); e != nil {
			return e
		}
		after, e := os.ReadFile(draft.File)
		if e != nil {
			return e
		}
		if string(after) == draft.Before {
			return nil
		}
		return o.approve(cmd, "Save script · "+j.Host+":"+path, map[string]string{"diff": service.Diff(draft.Before, string(after))}, service.Diff(draft.Before, string(after)), func(ctx context.Context) (any, string, error) {
			r, e := s.SaveScript(ctx, draft, string(after))
			return r, pretty(r), e
		})
	}}
	edit.Flags().StringVar(&scriptPath, "path", "", "Explicit target-side script path")
	scripts.AddCommand(edit)
	root.AddCommand(&cobra.Command{Use: "queue", Short: "Open lazypueue for the explicitly mapped connection", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if o.json || !tty() {
			return usage("queue requires a terminal")
		}
		c, e := o.load()
		if e != nil {
			return e
		}
		h, e := c.Host(o.hostID(c))
		if e != nil {
			return e
		}
		binary, e := exec.LookPath("lazypueue")
		if e != nil {
			return fmt.Errorf("lazypueue is not installed locally")
		}
		args := []string{}
		if h.LazypueueConnection != "" {
			args = append(args, "--connection", h.LazypueueConnection)
		} else if h.ID != "local" {
			return fmt.Errorf("set this host's lazypueue_connection to avoid opening a different queue")
		}
		if o.dry {
			return o.emit(cmd, args, transport.Join(append([]string{binary}, args...)))
		}
		return transport.Interactive(exec.CommandContext(cmd.Context(), binary, args...))
	}})
}

func (o *options) editManagedScript(cmd *cobra.Command, s *service.Service, j service.Entry, recipe service.Recipe) error {
	before, err := s.ReadManagedScript(cmd.Context(), j, recipe)
	if err != nil {
		return err
	}
	// Capture the normal edit workflow before the editor is opened. Its source
	// revision remains authoritative throughout the editor and review stages.
	spec, values, err := newJobFormSpec(cmd.Context(), s, j.Host, j.Source, "edit", j.ID, nil)
	if err != nil {
		return err
	}
	if values["preset"] != "managed-shell" || values["script"] != recipe.Script || values["script_content"] != before {
		return fmt.Errorf("managed script changed before editing; reload the job and try again")
	}
	draft, err := s.ScriptDraft(cmd.Context(), j.Host, recipe.Script)
	if err != nil {
		return err
	}
	defer os.Remove(draft.File)
	if draft.Before != before {
		return fmt.Errorf("managed script content changed before editing")
	}
	if err = transport.Interactive(editor(draft.File)); err != nil {
		return err
	}
	after, err := readManagedContentFile(draft.File)
	if err != nil {
		return err
	}
	if after == before {
		return nil
	}
	values["script_content"] = after
	review, err := spec.Build(cmd.Context(), values)
	if err != nil {
		return err
	}
	review.Text = "Script content changes\n" + service.Diff(before, after) + "\n\n" + review.Text
	return o.approve(cmd, "Save managed script · "+j.Key(), review.Data.(jobReview).Plan, review.Text, func(ctx context.Context) (any, string, error) {
		message, err := spec.Apply(ctx, values, review)
		return map[string]any{"job_id": review.Data.(jobReview).Plan.JobID, "result": message}, message, err
	})
}

func addBackup(root *cobra.Command, o *options) {
	group := &cobra.Command{Use: "backup", Short: "Inspect and restore original source snapshots"}
	root.AddCommand(group)
	group.AddCommand(&cobra.Command{Use: "list", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		backups, e := service.Backups()
		if e != nil {
			return e
		}
		type brief struct {
			ID, Host, Source string
			Time             time.Time
		}
		out := []brief{}
		for _, b := range backups {
			if o.host != "" && o.host != "all" && b.Host != o.host {
				continue
			}
			out = append(out, brief{b.ID, b.Host, b.Source, b.Time})
		}
		return o.emit(cmd, out, pretty(out))
	}})
	group.AddCommand(&cobra.Command{Use: "restore ID", Args: exactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		backups, err := service.Backups()
		if err != nil {
			return err
		}
		for _, b := range backups {
			if b.ID == args[0] && b.FilePath != "" {
				draft, err := s.ScriptDraft(cmd.Context(), b.Host, b.FilePath)
				if err != nil {
					return err
				}
				defer os.Remove(draft.File)
				return o.approve(cmd, "Restore script · "+b.Host+":"+b.FilePath, b, service.Diff(draft.Before, b.Content), func(ctx context.Context) (any, string, error) {
					r, e := s.SaveScript(ctx, draft, b.Content)
					return r, pretty(r), e
				})
			}
		}
		p, e := s.RestorePlan(cmd.Context(), args[0])
		if e != nil {
			return e
		}
		return o.applyPlan(cmd, s, p)
	}})
}
func addOverview(root *cobra.Command, o *options) {
	var week, timezone string
	var limit int
	cmd := &cobra.Command{Use: "overview", Short: "Forecast one week's triggers (Pueue jobs show enqueue times)", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		tz := timezone
		if tz == "" {
			tz = c.Timezone
		}
		loc, e := time.LoadLocation(tz)
		if e != nil {
			return usage("%v", e)
		}
		date := time.Now().In(loc)
		if week != "" {
			date, e = time.ParseInLocation("2006-01-02", week, loc)
			if e != nil {
				return usage("%v", e)
			}
		}
		s := service.New(c)
		snaps := s.List(cmd.Context(), o.hostID(c), o.sourceID(c))
		overview, e := service.BuildOverview(cmd.Context(), snaps, date, loc, limit)
		if e != nil {
			return e
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Forecast · week of %s · %s\n       Mon    Tue    Wed    Thu    Fri    Sat    Sun\n", overview.Start.Format("2006-01-02"), tz)
		for h := range 24 {
			fmt.Fprintf(&b, "%02d:00 ", h)
			for d := range 7 {
				cell := overview.Cells[d][h]
				label := fmt.Sprint(cell.Count)
				if cell.Truncated {
					label += "+"
				}
				fmt.Fprintf(&b, "%6s ", label)
			}
			b.WriteByte('\n')
		}
		for _, a := range overview.Agenda {
			fmt.Fprintf(&b, "%s  %s/%s  %s\n", a.Time.Format("Mon 15:04:05 -07:00"), a.Host, a.Source, a.JobID)
		}
		if overview.Truncated {
			b.WriteString("Agenda/counts are bounded; narrow the target or inspect a time cell in the TUI.\n")
		}
		for _, w := range overview.Warnings {
			b.WriteString("! " + w + "\n")
		}
		return o.emit(cmd, overview, b.String())
	}}
	cmd.Flags().StringVar(&week, "week", "", "Date within the requested week (YYYY-MM-DD)")
	cmd.Flags().StringVar(&timezone, "timezone", "", "IANA display timezone")
	cmd.Flags().IntVar(&limit, "limit", 200, "Maximum agenda rows")
	root.AddCommand(cmd)
}
