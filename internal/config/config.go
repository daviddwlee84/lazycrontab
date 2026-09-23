package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type Host struct {
	ID                  string `toml:"id" json:"id"`
	SSH                 string `toml:"ssh" json:"ssh,omitempty"`
	Timezone            string `toml:"timezone" json:"timezone,omitempty"`
	Pueue               string `toml:"pueue" json:"pueue,omitempty"`
	LazypueueConnection string `toml:"lazypueue_connection" json:"lazypueue_connection,omitempty"`
}
type Source struct {
	ID       string   `toml:"id" json:"id"`
	Host     string   `toml:"host" json:"host"`
	Kind     string   `toml:"kind" json:"kind"`
	Path     string   `toml:"path" json:"path,omitempty"`
	Dialect  string   `toml:"dialect" json:"dialect,omitempty"`
	Timezone string   `toml:"timezone" json:"timezone,omitempty"`
	Reload   []string `toml:"reload" json:"reload,omitempty"`
	ReadOnly bool     `toml:"read_only" json:"read_only"`
	CronTZ   bool     `toml:"cron_tz" json:"cron_tz"`
}
type Config struct {
	DefaultHost           string            `toml:"default_host" json:"default_host"`
	DefaultSource         string            `toml:"default_source" json:"default_source"`
	Locale                string            `toml:"locale" json:"locale"`
	Timezone              string            `toml:"timezone" json:"timezone"`
	Theme                 string            `toml:"theme" json:"theme"`
	Mouse                 bool              `toml:"mouse" json:"mouse"`
	RefreshSeconds        int               `toml:"refresh_seconds" json:"refresh_seconds"`
	ConnectTimeoutSeconds int               `toml:"connect_timeout_seconds" json:"connect_timeout_seconds"`
	MaxParallel           int               `toml:"max_parallel" json:"max_parallel"`
	Hosts                 []Host            `toml:"hosts,omitempty" json:"hosts"`
	Sources               []Source          `toml:"sources,omitempty" json:"sources"`
	Keys                  map[string]string `toml:"keys,omitempty" json:"keys,omitempty"`
	Path                  string            `toml:"-" json:"path"`
}

func Base(kind string) (string, error) {
	vars := map[string]string{"config": "XDG_CONFIG_HOME", "data": "XDG_DATA_HOME", "state": "XDG_STATE_HOME", "cache": "XDG_CACHE_HOME"}
	fallback := map[string]string{"config": ".config", "data": ".local/share", "state": ".local/state", "cache": ".cache"}
	if vars[kind] == "" {
		return "", fmt.Errorf("unknown XDG category")
	}
	if p := os.Getenv(vars[kind]); filepath.IsAbs(p) {
		return filepath.Join(p, "lazycrontab"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("HOME unavailable")
	}
	return filepath.Join(home, fallback[kind], "lazycrontab"), nil
}
func Path(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p := os.Getenv("LAZYCRONTAB_CONFIG"); p != "" {
		return p, nil
	}
	base, err := Base("config")
	return filepath.Join(base, "config.toml"), err
}
func Defaults() Config {
	return Config{DefaultHost: "local", DefaultSource: "user", Locale: "en", Timezone: "Local", Theme: "auto", Mouse: true, RefreshSeconds: 30, ConnectTimeoutSeconds: 10, MaxParallel: 4, Hosts: []Host{}, Sources: []Source{}, Keys: map[string]string{}}
}
func Load(explicit string) (Config, error) {
	c := Defaults()
	path, err := Path(explicit)
	if err != nil {
		return c, err
	}
	c.Path = path
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || explicit != "" || os.Getenv("LAZYCRONTAB_CONFIG") != "" {
			return c, fmt.Errorf("read config %s: %w", path, err)
		}
	} else if err = toml.NewDecoder(bytes.NewReader(b)).DisallowUnknownFields().Decode(&c); err != nil {
		return c, fmt.Errorf("parse config: %w (repair with config edit)", err)
	}
	c.Path = path
	if v := os.Getenv("LAZYCRONTAB_HOST"); v != "" {
		c.DefaultHost = v
	}
	if v := os.Getenv("LAZYCRONTAB_SOURCE"); v != "" {
		c.DefaultSource = v
	}
	if v := os.Getenv("LAZYCRONTAB_LOCALE"); v != "" {
		c.Locale = v
	}
	return c, c.Validate()
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func (c Config) Validate() error {
	if c.MaxParallel < 1 || c.MaxParallel > 32 || c.ConnectTimeoutSeconds < 1 || c.RefreshSeconds < 1 {
		return fmt.Errorf("parallelism must be 1..32; timeouts and refresh must be positive")
	}
	if c.Locale != "en" && c.Locale != "zh_TW" {
		return fmt.Errorf("locale must be en or zh_TW")
	}
	if c.Theme != "auto" && c.Theme != "dark" && c.Theme != "light" {
		return fmt.Errorf("theme must be auto, dark or light")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, h := range c.Hosts {
		if !safeID.MatchString(h.ID) || h.ID == "all" || seen[h.ID] {
			return fmt.Errorf("invalid or duplicate host ID %q", h.ID)
		}
		seen[h.ID] = true
		if h.ID != "local" && (h.SSH == "" || strings.HasPrefix(h.SSH, "-") || strings.ContainsAny(h.SSH, "\r\n\x00")) {
			return fmt.Errorf("host %s needs a valid SSH destination", h.ID)
		}
		if h.ID == "local" && h.SSH != "" {
			return fmt.Errorf("local host cannot have SSH destination")
		}
		if h.Timezone != "" {
			if _, e := time.LoadLocation(h.Timezone); e != nil {
				return e
			}
		}
	}
	seen = map[string]bool{}
	for _, s := range c.Sources {
		if !safeID.MatchString(s.ID) || s.ID == "all" || seen[s.Host+"/"+s.ID] {
			return fmt.Errorf("invalid or duplicate source ID %q", s.ID)
		}
		seen[s.Host+"/"+s.ID] = true
		if _, e := c.Host(s.Host); e != nil {
			return e
		}
		if s.Kind != "user" && s.Kind != "file" && s.Kind != "system" {
			return fmt.Errorf("source kind must be user, file, or system")
		}
		if s.Kind != "user" && s.Path == "" {
			return fmt.Errorf("file source requires path")
		}
		if s.Path != "" && (!filepath.IsAbs(s.Path) && !strings.HasPrefix(s.Path, "~/") || strings.ContainsAny(s.Path, "\r\n\x00")) {
			return fmt.Errorf("source path must be absolute or start with ~/")
		}
		if s.Kind != "file" && s.Dialect != "" && s.Dialect != "system" {
			return fmt.Errorf("user/system sources require the system dialect")
		}
		if len(s.Reload) > 0 && s.Reload[0] == "" {
			return fmt.Errorf("reload executable cannot be empty")
		}
		if s.Dialect != "" && s.Dialect != "system" && s.Dialect != "supercronic" {
			return fmt.Errorf("invalid source dialect")
		}
		if s.Timezone != "" {
			if _, e := time.LoadLocation(s.Timezone); e != nil {
				return e
			}
		}
	}
	return nil
}
func (c Config) Host(id string) (Host, error) {
	for _, h := range c.Hosts {
		if h.ID == id {
			return h, nil
		}
	}
	if id == "local" {
		return Host{ID: "local"}, nil
	}
	return Host{}, fmt.Errorf("unknown host %q; use hosts add", id)
}
func (c Config) AllHosts() []Host {
	hs := []Host{}
	h, _ := c.Host("local")
	hs = append(hs, h)
	for _, h := range c.Hosts {
		if h.ID != "local" {
			hs = append(hs, h)
		}
	}
	return hs
}
func (c Config) HostSources(host string) []Source {
	ss := []Source{}
	hasUser := false
	for _, s := range c.Sources {
		if s.Host == host {
			ss = append(ss, s)
			if s.ID == "user" {
				hasUser = true
			}
		}
	}
	if !hasUser {
		ss = append([]Source{{ID: "user", Host: host, Kind: "user", Dialect: "system"}}, ss...)
	}
	return ss
}
func (c Config) Source(host, id string) (Source, error) {
	for _, s := range c.HostSources(host) {
		if s.ID == id {
			return s, nil
		}
	}
	return Source{}, fmt.Errorf("unknown source %s/%s", host, id)
}

func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".lazycrontab-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

// SaveEntity edits only an explicitly selected table; other tables and top-level
// preferences retain their original bytes. Flat fields retain inline comments.
func SaveEntity(path, kind, id, host string, value any, remove bool) error {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	before := string(raw)
	newData, err := toml.Marshal(value)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^\[.*\][^\n]*\n`)
	locs := re.FindAllStringIndex(before, -1)
	start, end := -1, -1
	for i, r := range locs {
		e := len(before)
		if i+1 < len(locs) {
			e = locs[i+1][0]
		}
		block := before[r[1]:e]
		var m map[string]any
		if toml.Unmarshal([]byte(block), &m) != nil {
			continue
		}
		if m["id"] == id && (kind == "hosts" || m["host"] == host) && strings.Contains(before[r[0]:r[1]], "[["+kind+"]]") {
			start, end = r[0], e
			break
		}
	}
	after := before
	if start >= 0 {
		if remove {
			after = before[:start] + before[end:]
		} else {
			old := before[start:end]
			fields := strings.Split(strings.TrimSpace(string(newData)), "\n")
			for _, line := range fields {
				k, _, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				key := strings.TrimSpace(k)
				fieldRE := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=.*$`)
				if found := fieldRE.FindString(old); found != "" {
					suffix := inlineComment(found)
					old = fieldRE.ReplaceAllStringFunc(old, func(string) string { return line + suffix })
				} else {
					old += line + "\n"
				}
			}
			after = before[:start] + old + before[end:]
		}
	} else {
		if remove {
			return fmt.Errorf("explicit %s entry %s not found", kind, id)
		}
		if after != "" && !strings.HasSuffix(after, "\n") {
			after += "\n"
		}
		after += "\n[[" + kind + "]]\n" + string(newData)
	}
	test := Defaults()
	if err := toml.Unmarshal([]byte(after), &test); err != nil {
		return err
	}
	if err := test.Validate(); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if string(current) != before {
		return fmt.Errorf("config changed during edit; retry")
	}
	return AtomicWrite(path, []byte(after), 0600)
}
func inlineComment(s string) string {
	var quote rune
	escaped := false
	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
		}
		if r == '#' {
			return " " + s[i:]
		}
	}
	return ""
}
