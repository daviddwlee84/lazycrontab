package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/daviddwlee84/lazycrontab/internal/completioncache"
	"github.com/daviddwlee84/lazycrontab/internal/config"
	concept "github.com/daviddwlee84/lazycrontab/internal/help"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Completion only reads configuration, disposable job metadata and local backup
// records. It must never refresh a snapshot, inspect SSH or resolve credentials.
func configureCompletions(root *cobra.Command, o *options) {
	root.InitDefaultHelpCmd()
	var visit func(*cobra.Command)
	visit = func(cmd *cobra.Command) {
		// Every positional argument in the current command tree is a logical ID,
		// cron expression or help topic. Local paths are explicit flags below.
		cmd.ValidArgsFunction = cobra.NoFileCompletions
		setFlags := func(flags *pflag.FlagSet) {
			flags.VisitAll(func(flag *pflag.Flag) {
				completion := cobra.CompletionFunc(cobra.NoFileCompletions)
				switch flag.Name {
				case "config", "file", "script-content-file":
					completion = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
						return nil, cobra.ShellCompDirectiveDefault
					}
				case "host":
					completion = o.completeHostFlag
				case "source":
					completion = o.completeSourceFlag
				case "runner":
					completion = completeEnum("direct", "pueue")
				case "output-policy":
					completion = completeEnum(outputPolicies...)
				case "enqueue-output":
					completion = completeEnum(enqueueOutputs...)
				case "preset":
					completion = completeEnum(jobPresets()...)
				case "kind":
					completion = completeEnum("user", "file", "system")
				case "dialect":
					completion = completeEnum("system", "supercronic")
				case "color":
					completion = completeEnum("auto", "always", "never")
				case "locale":
					completion = completeEnum("en", "zh_TW")
				case "from":
					if cmd.Parent() != nil && cmd.Parent().Name() == "hosts" {
						completion = completeEnum("ssh", "dev")
					}
				}
				_ = cmd.RegisterFlagCompletionFunc(flag.Name, completion)
			})
		}
		setFlags(cmd.LocalNonPersistentFlags())
		setFlags(cmd.PersistentFlags())
		path := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
		switch path {
		case "sources edit", "sources remove":
			cmd.ValidArgsFunction = firstArgument(o.completeSourceID(cmd.Name() == "remove"))
		case "hosts edit", "hosts remove", "hosts test", "hosts authenticate":
			cmd.ValidArgsFunction = firstArgument(o.completeHostID(cmd.Name()))
		case "show", "edit", "enable", "disable", "remove", "run", "logs", "check", "script edit":
			cmd.ValidArgsFunction = firstArgument(o.completeJobID(path))
		case "backup restore":
			cmd.ValidArgsFunction = firstArgument(o.completeBackupID)
		case "completion":
			cmd.ValidArgsFunction = firstArgument(completeEnum("bash", "zsh", "fish", "powershell"))
		case "help":
			cmd.ValidArgsFunction = completeHelp(root)
		}
		for _, child := range cmd.Commands() {
			visit(child)
		}
	}
	visit(root)
}

func firstArgument(complete cobra.CompletionFunc) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return complete(cmd, args, prefix)
	}
}

func completeEnum(values ...string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		return completionResults(values, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}

func (o *options) completeHostFlag(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	c, err := config.Load(o.config)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	choices := []string{"all\tAll registered hosts"}
	for _, h := range c.AllHosts() {
		choices = append(choices, hostCompletion(h))
	}
	return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
}

func (o *options) completeSourceFlag(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	c, err := config.Load(o.config)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	host := o.hostID(c)
	if host != "all" {
		if _, err := c.Host(host); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	choices := []string{"all\tAll sources on the selected host"}
	for _, h := range c.AllHosts() {
		if host != "all" && host != h.ID {
			continue
		}
		for _, src := range c.HostSources(h.ID) {
			choices = append(choices, sourceCompletion(src))
		}
	}
	return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
}

func (o *options) completeSourceID(registeredOnly bool) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		c, err := config.Load(o.config)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		host := o.hostID(c)
		if _, err := c.Host(host); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		sources := c.HostSources(host)
		if registeredOnly {
			sources = c.Sources
		}
		choices := []string{}
		for _, src := range sources {
			if src.Host == host {
				choices = append(choices, sourceCompletion(src))
			}
		}
		return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}

func (o *options) completeHostID(operation string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		c, err := config.Load(o.config)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		hosts := c.AllHosts()
		if operation == "remove" {
			hosts = c.Hosts
		}
		choices := []string{}
		for _, h := range hosts {
			if operation == "authenticate" && h.SSH == "" {
				continue
			}
			choices = append(choices, hostCompletion(h))
		}
		return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}

func (o *options) completeJobID(operation string) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		c, err := config.Load(o.config)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		choices := []string{}
		for _, e := range completioncache.Load(c, o.hostID(c), o.sourceID(c)) {
			mutable := operation == "edit" || operation == "enable" || operation == "disable" || operation == "remove" || operation == "run" || operation == "script edit"
			if mutable && e.ReadOnly || operation == "enable" && e.Enabled || operation == "disable" && !e.Enabled {
				continue
			}
			status := "disabled"
			if e.Enabled {
				status = "enabled"
			}
			if e.ReadOnly {
				status += ", read-only"
			}
			status += " · cached"
			label := status
			if e.Name != "" {
				label = e.Name + " · " + status
			}
			choices = append(choices, completionChoice(e.ID, label))
		}
		return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}

func (o *options) completeBackupID(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if _, err := config.Load(o.config); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	base, err := config.Base("state")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	files, err := filepath.Glob(filepath.Join(base, "backups", "*.json"))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Timestamp-based backup names sort newest last. Bound metadata reads and
	// stop before their possibly large command/script content fields.
	if len(files) > 256 {
		files = files[len(files)-256:]
	}
	choices := []string{}
	for _, file := range files {
		backup, ok := readBackupCompletion(file)
		if !ok {
			continue
		}
		if o.host != "" && o.host != "all" && backup.Host != o.host || o.source != "" && o.source != "all" && backup.Source != o.source {
			continue
		}
		label := backup.Host + "/" + backup.Source
		if !backup.Time.IsZero() {
			label += " · " + backup.Time.Format("2006-01-02 15:04")
		}
		choices = append(choices, completionChoice(backup.ID, label))
	}
	return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
}

type backupCompletion struct {
	ID, Host, Source string
	Time             time.Time
}

func readBackupCompletion(path string) (backupCompletion, bool) {
	var result backupCompletion
	if stat, err := os.Lstat(path); err != nil || !stat.Mode().IsRegular() {
		return result, false
	}
	file, err := os.Open(path)
	if err != nil {
		return result, false
	}
	defer file.Close()
	if stat, err := file.Stat(); err != nil || !stat.Mode().IsRegular() {
		return result, false
	}
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return result, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return result, false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return result, false
		}
		seen[key] = true
		switch key {
		case "id":
			err = decoder.Decode(&result.ID)
		case "host":
			err = decoder.Decode(&result.Host)
		case "source":
			err = decoder.Decode(&result.Source)
		case "time":
			err = decoder.Decode(&result.Time)
		default:
			var ignored json.RawMessage
			err = decoder.Decode(&ignored)
		}
		if err != nil {
			return result, false
		}
		if seen["id"] && seen["host"] && seen["source"] && seen["time"] {
			return result, result.ID != "" && result.Host != "" && result.Source != ""
		}
	}
	return result, false
}

func hostCompletion(h config.Host) string {
	label := "local host"
	if h.SSH != "" {
		label = "SSH " + h.SSH
	}
	return completionChoice(h.ID, label)
}

func sourceCompletion(src config.Source) string {
	label := src.Host + " · " + src.Kind
	if src.ReadOnly || src.Kind == "system" {
		label += " · read-only"
	}
	return completionChoice(src.ID, label)
}

func completionChoice(value, label string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		if unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, ansi.Strip(label))
	clean = strings.Join(strings.Fields(clean), " ")
	if runes := []rune(clean); len(runes) > 120 {
		clean = string(runes[:117]) + "..."
	}
	if clean == "" {
		return value
	}
	return value + "\t" + clean
}

func completionResults(choices []string, prefix string) []string {
	sort.Strings(choices)
	result := []string{}
	last := ""
	for _, choice := range choices {
		value, _, _ := strings.Cut(choice, "\t")
		if !validCompletionID(value) || value == last || !strings.HasPrefix(value, prefix) {
			continue
		}
		last = value
		result = append(result, choice)
	}
	return result
}

func validCompletionID(value string) bool {
	return value != "" && !strings.HasPrefix(value, "-") && !strings.ContainsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.')
	})
}

func completeHelp(root *cobra.Command) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		choices := []string{}
		if len(args) == 0 {
			choices = append(choices, "topics\tBrowse cron concepts")
			for _, topic := range concept.Topics() {
				choices = append(choices, completionChoice(topic.Name, topic.Summary))
			}
		}
		cmd, rest, err := root.Find(args)
		if err == nil && len(rest) == 0 {
			for _, child := range cmd.Commands() {
				if child.IsAvailableCommand() {
					choices = append(choices, completionChoice(child.Name(), child.Short))
				}
			}
		}
		return completionResults(choices, prefix), cobra.ShellCompDirectiveNoFileComp
	}
}
