package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/daviddwlee84/lazycrontab/internal/hostinventory"
	"github.com/daviddwlee84/lazycrontab/internal/service"
	"github.com/daviddwlee84/lazycrontab/internal/transport"
	"github.com/daviddwlee84/lazycrontab/internal/ui"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func editor(path string) *exec.Cmd {
	value := os.Getenv("VISUAL")
	if value == "" {
		value = os.Getenv("EDITOR")
	}
	if value == "" {
		value = "vi"
	}
	args, err := splitArgs(value)
	if err != nil || len(args) == 0 {
		return exec.Command("vi", path)
	}
	return exec.Command(args[0], append(args[1:], path)...)
}

// splitArgs supports quoted editor arguments, without shell expansion/evaluation.
func splitArgs(value string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range value {
		if escaped {
			b.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			started = true
			continue
		}
		if r == ' ' || r == '\t' {
			if started {
				out = append(out, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unterminated editor quoting")
	}
	if started {
		out = append(out, b.String())
	}
	return out, nil
}
func addConfig(root *cobra.Command, o *options) {
	group := &cobra.Command{Use: "config", Short: "Inspect and edit XDG preferences"}
	root.AddCommand(group)
	group.AddCommand(&cobra.Command{Use: "path", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		p, e := config.Path(o.config)
		if e != nil {
			return e
		}
		return o.emit(cmd, map[string]string{"path": p}, p)
	}})
	group.AddCommand(&cobra.Command{Use: "show", Short: "Show effective configuration and its path", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		return o.emit(cmd, c, pretty(c))
	}})
	group.AddCommand(&cobra.Command{Use: "init", Short: "Create a default configuration after review", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		p, e := config.Path(o.config)
		if e != nil {
			return e
		}
		if _, e = os.Stat(p); e == nil {
			return fmt.Errorf("config already exists; use config edit")
		}
		c := config.Defaults()
		b, e := toml.Marshal(c)
		if e != nil {
			return e
		}
		return o.approve(cmd, "Initialize config", map[string]string{"path": p, "content": string(b)}, p+"\n"+string(b), func(context.Context) (any, string, error) {
			if _, err := os.Stat(p); err == nil {
				return nil, "", fmt.Errorf("config now exists")
			}
			e := config.AtomicWrite(p, b, 0600)
			return map[string]string{"path": p}, "Saved " + p, e
		})
	}})
	group.AddCommand(&cobra.Command{Use: "edit", Short: "Open config in VISUAL/EDITOR, including malformed files", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if o.json || !tty() {
			return usage("config edit needs a terminal")
		}
		p, e := config.Path(o.config)
		if e != nil {
			return e
		}
		if o.dry {
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		}
		if _, e = os.Stat(p); os.IsNotExist(e) {
			b, _ := toml.Marshal(config.Defaults())
			if e = config.AtomicWrite(p, b, 0600); e != nil {
				return e
			}
		}
		if e = transport.Interactive(editor(p)); e != nil {
			return e
		}
		_, e = config.Load(p)
		return e
	}})
}
func addHosts(root *cobra.Command, o *options) {
	group := &cobra.Command{Use: "hosts", Short: "Register and inspect SSH hosts"}
	root.AddCommand(group)
	group.AddCommand(&cobra.Command{Use: "list", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		return o.emit(cmd, c.AllHosts(), pretty(c.AllHosts()))
	}})
	group.AddCommand(&cobra.Command{Use: "test [ID]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		id := o.hostID(c)
		if len(args) > 0 {
			id = args[0]
		}
		s := service.New(c)
		snaps := s.List(cmd.Context(), id, "user")
		return o.emit(cmd, snaps, pretty(snaps))
	}})
	group.AddCommand(&cobra.Command{Use: "authenticate [ID]", Short: "Hand the terminal to native SSH authentication", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if o.json || !tty() {
			return usage("authentication requires a terminal")
		}
		c, e := o.load()
		if e != nil {
			return e
		}
		id := o.hostID(c)
		if len(args) > 0 {
			id = args[0]
		}
		h, e := c.Host(id)
		if e != nil {
			return e
		}
		if h.SSH == "" {
			return fmt.Errorf("local host does not use SSH")
		}
		if o.dry {
			return o.emit(cmd, h, "ssh "+h.SSH+" true")
		}
		child, message, e := (transport.Native{}).Authentication(cmd.Context(), h)
		if e != nil {
			return e
		}
		fmt.Fprintln(cmd.OutOrStdout(), message)
		if err := transport.Interactive(child); err != nil {
			return err
		}
		// One successful interactive login does not prove that a later batch
		// connection can reuse it (for example, ControlPersist may be disabled).
		snapshot, err := service.New(c).Snapshot(cmd.Context(), id, "user")
		if err != nil {
			return fmt.Errorf("native SSH completed, but the background crontab read still failed: %w; check the SSH sharing policy or load the key into your agent", err)
		}
		return o.emit(cmd, snapshot, fmt.Sprintf("SSH ready · %s · %d jobs", id, len(snapshot.Entries)))
	}})
	for _, op := range []string{"add", "edit", "remove"} {
		addEntity(group, o, "hosts", op)
	}
	var discoverySource string
	discoverCmd := &cobra.Command{Use: "discover", Short: "List SSH alias candidates without connecting", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if discoverySource != "ssh" && discoverySource != "dev" {
			return usage("--from must be ssh or dev")
		}
		inv, err := hostinventory.Discover(cmd.Context(), discoverySource)
		if err != nil {
			return err
		}
		var body strings.Builder
		for _, c := range inv.Candidates {
			fmt.Fprintf(&body, "%s\t%s\n", c.Alias, c.Status)
		}
		if len(inv.Candidates) == 0 {
			body.WriteString("No exact SSH aliases found. Use hosts add --interactive to enter one.\n")
		}
		if !inv.Complete {
			body.WriteString("Discovery is incomplete; uncertain entries require an explicit manual alias.\n")
		}
		for _, diagnostic := range inv.Diagnostics {
			fmt.Fprintln(&body, diagnostic)
		}
		return o.emit(cmd, inv, strings.TrimSpace(body.String()))
	}}
	discoverCmd.Flags().StringVar(&discoverySource, "from", "ssh", "ssh or dev (static local inventory)")
	group.AddCommand(discoverCmd)
	var from string
	var aliases []string
	importCmd := &cobra.Command{Use: "import", Short: "Choose which SSH aliases to register", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		if from != "ssh" && from != "dev" {
			return usage("--from must be ssh or dev")
		}
		c, err := o.load()
		if err != nil {
			return err
		}
		if tty() && !o.json && !o.dry && !o.yes && len(aliases) == 0 {
			return ui.RunHostPicker(cmd.Context(), c, ui.HostPickerOptions{Source: from})
		}
		inv, err := hostinventory.Discover(cmd.Context(), from)
		if err != nil {
			return err
		}
		restricted := len(aliases) > 0
		requested := map[string]bool{}
		for _, alias := range aliases {
			requested[alias] = false
		}
		registered := map[string]bool{}
		for _, h := range c.AllHosts() {
			registered[h.SSH] = true
		}
		selected := []config.Host{}
		for _, candidate := range inv.Candidates {
			if restricted {
				if _, ok := requested[candidate.Alias]; !ok {
					continue
				}
			}
			if !candidate.Selectable {
				continue
			}
			requested[candidate.Alias] = true
			if registered[candidate.Alias] {
				continue
			}
			if _, e := c.Host(candidate.Alias); e == nil {
				continue
			}
			selected = append(selected, config.Host{ID: candidate.Alias, SSH: candidate.Alias})
		}
		for alias, found := range requested {
			if !found {
				return usage("alias %q is missing or uncertain; use hosts add ID --ssh ALIAS for an explicit registration", alias)
			}
		}
		if len(selected) == 0 {
			return o.emit(cmd, selected, "No new selectable hosts to register")
		}
		plan, err := service.PlanHosts(c, selected)
		if err != nil {
			return err
		}
		return o.approve(cmd, "Register selected SSH hosts", plan, plan.Description(), func(ctx context.Context) (any, string, error) {
			err := service.ApplyHosts(ctx, plan)
			return selected, fmt.Sprintf("Registered %d hosts; use hosts test ID to check a connection", len(selected)), err
		})
	}}
	importCmd.Flags().StringVar(&from, "from", "ssh", "ssh or dev")
	importCmd.Flags().StringSliceVar(&aliases, "alias", nil, "Specific aliases to import (repeat or comma-separate)")
	group.AddCommand(importCmd)
	root.AddCommand(&cobra.Command{Use: "doctor", Short: "Inspect target cron and optional Pueue capabilities", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		type item struct {
			Host   string               `json:"host"`
			Source service.Snapshot     `json:"source"`
			Pueue  service.Capabilities `json:"pueue"`
		}
		items := []item{}
		for _, h := range c.AllHosts() {
			if o.hostID(c) != "all" && o.hostID(c) != h.ID {
				continue
			}
			snap, e := s.Snapshot(cmd.Context(), h.ID, "user")
			if e != nil {
				snap.Error = e.Error()
			}
			items = append(items, item{h.ID, snap, s.Capabilities(cmd.Context(), h.ID)})
		}
		return o.emit(cmd, items, pretty(items))
	}})
}
func addSources(root *cobra.Command, o *options) {
	group := &cobra.Command{Use: "sources", Short: "Manage user crontab and file source registrations"}
	root.AddCommand(group)
	group.AddCommand(&cobra.Command{Use: "list", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		out := []config.Source{}
		for _, h := range c.AllHosts() {
			if o.hostID(c) == "all" || o.hostID(c) == h.ID {
				out = append(out, c.HostSources(h.ID)...)
			}
		}
		return o.emit(cmd, out, pretty(out))
	}})
	for _, op := range []string{"add", "edit", "remove"} {
		addEntity(group, o, "sources", op)
	}
	group.AddCommand(&cobra.Command{Use: "reload", Short: "Run this source's explicitly configured reload argv", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		s := service.New(c)
		src, e := c.Source(o.hostID(c), o.sourceID(c))
		if e != nil {
			return e
		}
		if len(src.Reload) == 0 {
			return fmt.Errorf("configure a reload argv first")
		}
		return o.approve(cmd, "Reload "+src.Host+"/"+src.ID, src.Reload, transport.Join(src.Reload), func(ctx context.Context) (any, string, error) {
			r, e := s.Reload(ctx, src.Host, src.ID)
			return r, pretty(r), e
		})
	}})
}
func addEntity(parent *cobra.Command, o *options, kind, op string) {
	var ssh, timezone, path, dialect, sourceKind, reload, pueue, connection, inventorySource string
	var readOnly bool
	cmd := &cobra.Command{Use: op + " [ID]", Short: op + " a " + kind + " registration", Args: cobra.MaximumNArgs(1)}
	f := cmd.Flags()
	f.StringVar(&timezone, "timezone", "", "IANA target timezone")
	if kind == "hosts" {
		if op == "add" {
			f.StringVar(&inventorySource, "from", "ssh", "Alias picker source: ssh or dev")
		}
		f.StringVar(&ssh, "ssh", "", "OpenSSH alias or destination")
		f.StringVar(&pueue, "pueue", "", "Target Pueue executable path")
		f.StringVar(&connection, "lazypueue-connection", "", "Local lazypueue connection ID")
	} else {
		f.StringVar(&sourceKind, "kind", "file", "user, file, or system (read-only)")
		f.StringVar(&path, "path", "", "Target-side file path")
		f.StringVar(&dialect, "dialect", "supercronic", "system or supercronic")
		f.StringVar(&reload, "reload", "", "Reload argv as a JSON string array")
		f.BoolVar(&readOnly, "read-only", false, "Disable source writes")
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if kind == "sources" && f.Changed("kind") && !f.Changed("dialect") && sourceKind != "file" {
			dialect = "system"
		}
		if timezone != "" {
			if _, e := time.LoadLocation(timezone); e != nil {
				return usage("%v", e)
			}
		}
		if kind == "sources" && (sourceKind != "user" && sourceKind != "file" && sourceKind != "system") {
			return usage("invalid source kind")
		}
		if kind == "sources" && dialect != "system" && dialect != "supercronic" {
			return usage("invalid dialect")
		}
		if reload != "" {
			var a []string
			if e := json.Unmarshal([]byte(reload), &a); e != nil {
				return usage("reload must be a JSON argv array")
			}
		}
		if kind == "hosts" && op == "add" && inventorySource != "ssh" && inventorySource != "dev" {
			return usage("--from must be ssh or dev")
		}
		c, e := o.load()
		if e != nil {
			return e
		}
		if kind == "hosts" && op == "add" && len(args) == 0 && tty() && !o.json && !o.dry {
			fieldFlags := false
			f.Visit(func(flag *pflag.Flag) {
				if flag.Name != "from" && cmd.Root().PersistentFlags().Lookup(flag.Name) == nil {
					fieldFlags = true
				}
			})
			if !fieldFlags {
				return ui.RunHostPicker(cmd.Context(), c, ui.HostPickerOptions{Source: inventorySource})
			}
		}
		host := o.hostID(c)
		id := ""
		if len(args) > 0 {
			id = args[0]
		}
		if op != "add" && id == "" {
			return usage("%s needs an ID", op)
		}
		if kind == "sources" && host == "all" {
			return usage("choose one host")
		}
		h := config.Host{ID: id}
		src := config.Source{ID: id, Host: host, Kind: sourceKind, Dialect: dialect}
		if op != "add" {
			if kind == "hosts" {
				h, e = c.Host(id)
			} else {
				src, e = c.Source(host, id)
			}
			if e != nil {
				return e
			}
		}
		assign := func(flag string, target *string, value string) {
			if op == "add" || f.Changed(flag) {
				*target = value
			}
		}
		assign("ssh", &h.SSH, ssh)
		assign("pueue", &h.Pueue, pueue)
		assign("lazypueue-connection", &h.LazypueueConnection, connection)
		assign("timezone", &h.Timezone, timezone)
		assign("timezone", &src.Timezone, timezone)
		assign("path", &src.Path, path)
		assign("kind", &src.Kind, sourceKind)
		assign("dialect", &src.Dialect, dialect)
		if f.Changed("read-only") {
			src.ReadOnly = readOnly
		}
		if f.Changed("reload") {
			_ = json.Unmarshal([]byte(reload), &src.Reload)
		}
		if op == "remove" {
			if kind == "hosts" && id == c.DefaultHost && id != "local" {
				return fmt.Errorf("set default_host to another host in config before removing %s", id)
			}
			if kind == "sources" && host == c.DefaultHost && id == c.DefaultSource && id != "user" {
				return fmt.Errorf("set default_source to another source before removing %s", id)
			}
			value := any(h)
			if kind == "sources" {
				value = src
			}
			return o.approve(cmd, "Remove registration", value, pretty(value)+"\nOnly the registration is removed. Cron jobs remain.", func(context.Context) (any, string, error) {
				e := config.SaveEntity(c.Path, kind, id, host, value, true)
				return value, "Registration removed", e
			})
		}
		business := false
		f.Visit(func(flag *pflag.Flag) {
			if cmd.Root().PersistentFlags().Lookup(flag.Name) == nil {
				business = true
			}
		})
		wizard := o.interactive || (!business && (len(args) == 0 || op == "edit") && tty() && !o.json && !o.dry)
		spec, values := entityFormSpec(c, kind, op, id, host, h, src, wizard)
		if wizard {
			_, e = ui.RunForm(cmd.Context(), spec)
			return e
		}
		if id == "" {
			return usage("provide an ID and required fields, or --interactive")
		}
		r, e := spec.Build(cmd.Context(), values)
		if e != nil {
			return usage("%v; use --interactive for a wizard", e)
		}
		return o.approve(cmd, op+" "+kind, r.Data, r.Text, func(ctx context.Context) (any, string, error) {
			message, err := spec.Apply(ctx, values, r)
			return r.Data, message, err
		})
	}
	parent.AddCommand(cmd)
}

// entityFormSpec keeps standalone forms and embedded dashboard forms on the same
// validation and mutation path. Flag-only calls use the same Build and Apply.
func entityFormSpec(c config.Config, kind, op, id, host string, h config.Host, src config.Source, interactive bool) (ui.FormSpec, map[string]string) {
	values := map[string]string{"id": id, "ssh": h.SSH, "timezone": h.Timezone, "pueue": h.Pueue, "connection": h.LazypueueConnection, "kind": src.Kind, "path": src.Path, "dialect": src.Dialect, "reload": pretty(src.Reload), "readonly": strconv.FormatBool(src.ReadOnly)}
	if kind == "hosts" {
		values["timezone"] = h.Timezone
	} else {
		values["timezone"] = src.Timezone
	}
	build := func(_ context.Context, v map[string]string) (ui.Review, error) {
		var value any
		test := c
		if kind == "hosts" {
			item := config.Host{ID: v["id"], SSH: v["ssh"], Timezone: v["timezone"], Pueue: v["pueue"], LazypueueConnection: v["connection"]}
			value = item
			test.Hosts = []config.Host{}
			for _, old := range c.Hosts {
				if old.ID != id {
					test.Hosts = append(test.Hosts, old)
				}
			}
			test.Hosts = append(test.Hosts, item)
		} else {
			var argv []string
			if v["reload"] != "" && v["reload"] != "null" {
				if e := json.Unmarshal([]byte(v["reload"]), &argv); e != nil {
					return ui.Review{}, fmt.Errorf("reload must be a JSON argv array")
				}
			}
			item := config.Source{ID: v["id"], Host: host, Kind: v["kind"], Path: v["path"], Dialect: v["dialect"], Timezone: v["timezone"], Reload: argv, ReadOnly: v["readonly"] == "true", CronTZ: src.CronTZ}
			if item.Kind != "file" && interactive {
				item.Dialect = "system"
			}
			value = item
			test.Sources = []config.Source{}
			for _, old := range c.Sources {
				if old.ID != id || old.Host != host {
					test.Sources = append(test.Sources, old)
				}
			}
			test.Sources = append(test.Sources, item)
		}
		if e := test.Validate(); e != nil {
			return ui.Review{}, e
		}
		if op == "edit" && v["id"] != id {
			return ui.Review{}, fmt.Errorf("ID is immutable; add a new registration instead")
		}
		return ui.Review{Text: c.Path + "\n" + pretty(value), Data: value}, nil
	}
	apply := func(_ context.Context, v map[string]string, r ui.Review) (string, error) {
		e := config.SaveEntity(c.Path, kind, v["id"], host, r.Data, false)
		return "Saved " + kind + " " + v["id"], e
	}
	keys := []string{"id", "ssh", "timezone", "pueue", "connection"}
	if kind == "sources" {
		keys = []string{"id", "kind", "path", "dialect", "timezone", "reload", "readonly"}
	}
	fields := []ui.Field{}
	for _, key := range keys {
		field := ui.Field{Key: key, Label: key, Value: values[key]}
		if kind == "hosts" && (key == "timezone" || key == "pueue" || key == "connection") {
			field.Advanced = true
		}
		if kind == "hosts" && (key == "pueue" || key == "connection") && h.Pueue == "" && h.LazypueueConnection == "" {
			continue
		}
		if key == "kind" {
			field.Options = []string{"file", "user", "system"}
		}
		if key == "dialect" {
			field.Options = []string{"supercronic", "system"}
		}
		if key == "readonly" {
			field.Options = []string{"false", "true"}
		}
		fields = append(fields, field)
	}

	title := op + " " + kind
	if kind == "sources" {
		title += " · " + host
	}
	return ui.FormSpec{Title: title, Fields: fields, Build: build, Apply: apply, Mouse: c.Mouse, Theme: c.Theme}, values
}

func newSourceModel(ctx context.Context, cfg config.Config, host string) (tea.Model, error) {
	if host == "" {
		host = cfg.DefaultHost
	}
	if host == "all" {
		return nil, usage("choose one host before adding a source")
	}
	if _, err := cfg.Host(host); err != nil {
		return nil, err
	}
	source := config.Source{Host: host, Kind: "file", Dialect: "supercronic"}
	spec, _ := entityFormSpec(cfg, "sources", "add", "", host, config.Host{}, source, true)
	return ui.NewForm(ctx, spec), nil
}
