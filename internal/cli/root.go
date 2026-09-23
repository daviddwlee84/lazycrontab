package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type options struct {
	config, host, source, color string
	json, yes, dry, interactive bool
	version                     string
}
type usageError struct{ error }
type exitError struct {
	code int
	error
}

func usage(format string, args ...any) error { return usageError{fmt.Errorf(format, args...)} }
func tty() bool                              { return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) }
func Version(injected string) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if b, ok := debug.ReadBuildInfo(); ok && b.Main.Version != "" && b.Main.Version != "(devel)" {
		return b.Main.Version
	}
	return "dev"
}
func Execute(version string) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	o := &options{version: Version(version)}
	root := newRoot(o)
	err := root.ExecuteContext(ctx)
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		return 0
	}
	code := 1
	var u usageError
	var e exitError
	if errors.As(err, &u) {
		code = 2
	}
	if errors.As(err, &e) {
		code = e.code
	}
	if errors.Is(err, ui.ErrCancelled) || errors.Is(err, context.Canceled) {
		code = 130
		if !o.json {
			return code
		}
	}
	if o.json {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"error": err.Error(), "exit_code": code})
	} else {
		fmt.Fprintln(os.Stderr, "Error:", ui.Plain(err.Error()))
	}
	return code
}
func newRoot(o *options) *cobra.Command {
	root := &cobra.Command{Use: "lazycrontab", Short: "Understand and manage cron across local and SSH hosts", SilenceErrors: true, SilenceUsage: true, Version: o.version}
	f := root.PersistentFlags()
	f.StringVar(&o.config, "config", "", "XDG config file")
	f.StringVarP(&o.host, "host", "H", "", "Host ID (all for read-only fleet views)")
	f.StringVar(&o.source, "source", "", "Source ID (all for read-only views)")
	f.BoolVar(&o.json, "json", false, "Machine-readable output; never prompt")
	f.BoolVar(&o.yes, "yes", false, "Approve the requested operation without prompting")
	f.BoolVar(&o.dry, "dry-run", false, "Preview without writing or running jobs")
	f.BoolVar(&o.interactive, "interactive", false, "Open a wizard prefilled from flags")
	f.StringVar(&o.color, "color", "auto", "auto, always, or never")
	root.SetFlagErrorFunc(func(_ *cobra.Command, e error) error { return usage("%v", e) })
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if o.color != "auto" && o.color != "always" && o.color != "never" {
			return usage("--color must be auto, always or never")
		}
		if o.color == "never" {
			os.Setenv("NO_COLOR", "1")
		} else if o.color == "always" {
			os.Unsetenv("NO_COLOR")
		}
		if o.interactive && (o.json || !tty()) {
			return usage("--interactive requires an input/output terminal and cannot be combined with --json")
		}
		if o.interactive && o.dry {
			return usage("--interactive and --dry-run are separate entry paths")
		}
		return nil
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usage("unknown command %q; use --help", args[0])
		}
		if o.json {
			return usage("choose a data command, for example list --json")
		}
		if !tty() {
			return cmd.Help()
		}
		if o.dry {
			return usage("choose an operation for --dry-run, for example add --dry-run")
		}
		cfg, e := o.load()
		if e != nil {
			return e
		}
		return ui.Dashboard(cmd.Context(), service.New(cfg), o.hostID(cfg), o.sourceID(cfg), o.config, o.workflow)
	}
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the installed version (offline)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return o.emit(cmd, map[string]string{"version": o.version}, o.version)
	}})
	addSchedule(root, o)
	addJobs(root, o)
	addConfig(root, o)
	addHosts(root, o)
	addSources(root, o)
	addExecution(root, o)
	addBackup(root, o)
	addOverview(root, o)
	addUpgrade(root, o)
	addConceptHelp(root, o)
	completion := &cobra.Command{Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion without network access", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return root.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return root.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return root.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return root.GenPowerShellCompletion(cmd.OutOrStdout())
		default:
			return usage("unknown shell")
		}
	}}
	root.AddCommand(completion)
	_ = root.RegisterFlagCompletionFunc("host", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		c, e := config.Load(o.config)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids := []string{"all"}
		for _, h := range c.AllHosts() {
			ids = append(ids, h.ID)
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	})
	_ = root.RegisterFlagCompletionFunc("source", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		c, e := config.Load(o.config)
		if e != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids := []string{"all"}
		for _, s := range c.HostSources(o.hostID(c)) {
			ids = append(ids, s.ID)
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	})
	return root
}
func (o *options) load() (config.Config, error) {
	c, e := config.Load(o.config)
	if e != nil {
		return c, usage("%v", e)
	}
	host, source := o.hostID(c), o.sourceID(c)
	if host != "all" {
		if _, e := c.Host(host); e != nil {
			return c, usage("%v", e)
		}
		if source != "all" {
			if _, e := c.Source(host, source); e != nil {
				return c, usage("%v", e)
			}
		}
	}
	return c, nil
}
func (o *options) hostID(c config.Config) string {
	if o.host != "" {
		return o.host
	}
	return c.DefaultHost
}
func (o *options) sourceID(c config.Config) string {
	if o.source != "" {
		return o.source
	}
	return c.DefaultSource
}
func (o *options) emit(cmd *cobra.Command, data any, human string) error {
	if o.json {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(data)
	}
	fmt.Fprintln(cmd.OutOrStdout(), ui.Plain(human))
	return nil
}
func pretty(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func (o *options) approve(cmd *cobra.Command, title string, plan any, body string, apply func(context.Context) (any, string, error)) error {
	if o.dry {
		return o.emit(cmd, plan, body)
	}
	if o.yes {
		result, message, e := apply(cmd.Context())
		if result != nil {
			if out := o.emit(cmd, result, message); out != nil {
				return out
			}
		}
		return e
	}
	if o.json || !tty() {
		return usage("this operation requires --yes; use --dry-run to review first")
	}
	_, e := ui.Confirm(cmd.Context(), title, body, func(ctx context.Context) (string, error) { _, message, e := apply(ctx); return message, e })
	return e
}
func (o *options) applyPlan(cmd *cobra.Command, s *service.Service, p service.Plan) error {
	return o.approve(cmd, p.Operation+" · "+p.Host+"/"+p.Source, p, p.Diff+strings.Join(p.Warnings, "\n"), func(ctx context.Context) (any, string, error) { r, e := s.Apply(ctx, p); return r, pretty(r), e })
}
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usage("%s requires %d argument(s); see --help", cmd.CommandPath(), n)
		}
		return nil
	}
}
