package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
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
		return transport.Interactive(child)
	}})
	for _, op := range []string{"add", "edit", "remove"} {
		addEntity(group, o, "hosts", op)
	}
	var from string
	importCmd := &cobra.Command{Use: "import", Short: "Review SSH alias candidates without connecting", Args: exactArgs(0), RunE: func(cmd *cobra.Command, _ []string) error {
		c, e := o.load()
		if e != nil {
			return e
		}
		candidates := []config.Host{}
		switch from {
		case "ssh":
			home, e := os.UserHomeDir()
			if e != nil {
				return e
			}
			aliases := map[string]bool{}
			if e = scanSSH(filepath.Join(home, ".ssh", "config"), aliases, map[string]bool{}, 0); e != nil {
				return e
			}
			for alias := range aliases {
				candidates = append(candidates, config.Host{ID: alias, SSH: alias})
			}
		case "dev":
			raw, e := exec.CommandContext(cmd.Context(), "dev", "ssh", "list", "--json").Output()
			if e != nil {
				return e
			}
			var data any
			if e = json.Unmarshal(raw, &data); e != nil {
				return e
			}
			aliases := map[string]bool{}
			collectAliases(data, aliases)
			for alias := range aliases {
				candidates = append(candidates, config.Host{ID: alias, SSH: alias})
			}
		default:
			return usage("--from must be ssh or dev")
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		filtered := []config.Host{}
		for _, h := range candidates {
			if _, e := c.Host(h.ID); e != nil {
				test := c
				test.Hosts = append(append([]config.Host{}, c.Hosts...), h)
				if test.Validate() == nil {
					filtered = append(filtered, h)
				}
			}
		}
		return o.approve(cmd, "Import host registrations (no connections)", filtered, pretty(filtered), func(context.Context) (any, string, error) {
			for _, h := range filtered {
				if e := config.SaveEntity(c.Path, "hosts", h.ID, "", h, false); e != nil {
					return nil, "Previous registrations may have been saved.", e
				}
			}
			return filtered, fmt.Sprintf("Registered %d hosts", len(filtered)), nil
		})
	}}
	importCmd.Flags().StringVar(&from, "from", "ssh", "ssh or dev")
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
func scanSSH(path string, aliases, seen map[string]bool, depth int) error {
	if depth > 12 || seen[path] {
		return nil
	}
	seen[path] = true
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	home, _ := os.UserHomeDir()
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "host":
			for _, a := range f[1:] {
				if !strings.ContainsAny(a, "*?!#") {
					aliases[a] = true
				}
			}
		case "include":
			for _, p := range f[1:] {
				p = strings.Trim(p, "\"'")
				if strings.HasPrefix(p, "~/") {
					p = filepath.Join(home, p[2:])
				}
				if !filepath.IsAbs(p) {
					p = filepath.Join(home, ".ssh", p)
				}
				files, _ := filepath.Glob(p)
				for _, file := range files {
					if e := scanSSH(file, aliases, seen, depth+1); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
func collectAliases(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, value := range x {
			if k == "alias" || k == "ssh_alias" {
				if a, ok := value.(string); ok && a != "" && !strings.ContainsAny(a, "*?!") {
					out[a] = true
				}
			}
			collectAliases(value, out)
		}
	case []any:
		for _, v := range x {
			collectAliases(v, out)
		}
	}
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
	var ssh, timezone, path, dialect, sourceKind, reload, pueue, connection string
	var readOnly bool
	cmd := &cobra.Command{Use: op + " [ID]", Short: op + " a " + kind + " registration", Args: cobra.MaximumNArgs(1)}
	f := cmd.Flags()
	f.StringVar(&timezone, "timezone", "", "IANA target timezone")
	if kind == "hosts" {
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
		c, e := o.load()
		if e != nil {
			return e
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
		values := map[string]string{"id": id, "ssh": h.SSH, "timezone": timezone, "pueue": h.Pueue, "connection": h.LazypueueConnection, "kind": src.Kind, "path": src.Path, "dialect": src.Dialect, "reload": pretty(src.Reload), "readonly": strconv.FormatBool(src.ReadOnly)}
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
				if item.Kind != "file" && o.interactive {
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
		business := false
		f.Visit(func(flag *pflag.Flag) {
			if cmd.Root().PersistentFlags().Lookup(flag.Name) == nil {
				business = true
			}
		})
		wizard := o.interactive || (!business && (len(args) == 0 || op == "edit") && tty() && !o.json && !o.dry)
		if wizard {
			keys := []string{"id", "ssh", "timezone", "pueue", "connection"}
			if kind == "sources" {
				keys = []string{"id", "kind", "path", "dialect", "timezone", "reload", "readonly"}
			}
			fields := []ui.Field{}
			for _, key := range keys {
				field := ui.Field{Key: key, Label: key, Value: values[key]}
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
			_, e = ui.RunForm(cmd.Context(), ui.FormSpec{Title: op + " " + kind, Fields: fields, Build: build, Apply: apply, Mouse: c.Mouse})
			return e
		}
		if id == "" {
			return usage("provide an ID and required fields, or --interactive")
		}
		r, e := build(cmd.Context(), values)
		if e != nil {
			return usage("%v; use --interactive for a wizard", e)
		}
		return o.approve(cmd, op+" "+kind, r.Data, r.Text, func(ctx context.Context) (any, string, error) { msg, e := apply(ctx, values, r); return r.Data, msg, e })
	}
	parent.AddCommand(cmd)
}
