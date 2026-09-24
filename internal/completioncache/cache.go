// Package completioncache stores disposable job names and IDs for offline shell
// completion. Loading it never reads a crontab, probes a host, or writes a file.
package completioncache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

const (
	version  = 1
	maxBytes = 1 << 20
	maxAge   = 24 * time.Hour
)

// Entry deliberately excludes commands, environment values and script content.
type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Enabled  bool   `json:"enabled"`
	ReadOnly bool   `json:"read_only"`
}

type record struct {
	Version  int       `json:"version"`
	Identity string    `json:"identity"`
	Observed time.Time `json:"observed"`
	Entries  []Entry   `json:"entries"`
}

func location(cfg config.Config, host, source string) (path, identity string, err error) {
	if host == "all" || source == "all" {
		return "", "", fmt.Errorf("completion cache requires one target")
	}
	h, err := cfg.Host(host)
	if err != nil {
		return "", "", err
	}
	src, err := cfg.Source(host, source)
	if err != nil {
		return "", "", err
	}
	configPath := cfg.Path
	if configPath == "" {
		configPath, err = config.Path("")
		if err != nil {
			return "", "", err
		}
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return "", "", err
	}
	dialect := src.Dialect
	if dialect == "" {
		dialect = "system"
		if src.Kind == "file" {
			dialect = "supercronic"
		}
	}
	// Do not resolve an SSH alias or read its credentials. Its configured
	// destination, source identity, user and effective config scope suffice to
	// prevent a different configured target from reusing these suggestions.
	key, err := json.Marshal(struct {
		ConfigPath, Host, SSH, Source, Kind, Path, Dialect string
		UID                                                int
		ReadOnly                                           bool
	}{filepath.Clean(configPath), h.ID, h.SSH, src.ID, src.Kind, src.Path, dialect, os.Getuid(), src.ReadOnly || src.Kind == "system"})
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(key)
	identity = hex.EncodeToString(digest[:])
	base, err := config.Base("cache")
	if err != nil {
		return "", "", err
	}
	return filepath.Join(base, "completion", identity+".json"), identity, nil
}

func privateDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode().Perm()&0077 == 0
}

func validEntry(e Entry) bool {
	if e.ID == "" || len(e.ID) > 4096 || len(e.Name) > 4096 || !utf8.ValidString(e.ID) || !utf8.ValidString(e.Name) {
		return false
	}
	for _, text := range []string{e.ID, e.Name} {
		for _, r := range text {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}

// Load returns only a fresh, bounded local cache. Every failure is a silent miss;
// shell completion must remain usable when the target or cache is unavailable.
func Load(cfg config.Config, host, source string) []Entry {
	path, identity, err := location(cfg, host, source)
	if err != nil || !privateDir(filepath.Dir(path)) || !privateDir(filepath.Dir(filepath.Dir(path))) {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxBytes {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(content) > maxBytes {
		return nil
	}
	var r record
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&r); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	now := time.Now()
	if r.Version != version || r.Identity != identity || r.Observed.IsZero() || r.Observed.After(now) || now.Sub(r.Observed) > maxAge || len(r.Entries) > 10000 {
		return nil
	}
	seen := map[string]bool{}
	for _, e := range r.Entries {
		if !validEntry(e) || seen[e.ID] {
			return nil
		}
		seen[e.ID] = true
	}
	return r.Entries
}

// Save is best effort and writes only a minimal projection of a successful
// observation. Invalid records invalidate earlier suggestions instead of
// leaving a potentially misleading old list.
func Save(cfg config.Config, host, source string, entries []Entry) {
	path, identity, err := location(cfg, host, source)
	if err != nil {
		return
	}
	clean := make([]Entry, 0, min(len(entries), 10000))
	counts := map[string]int{}
	for _, e := range entries {
		counts[e.ID]++
	}
	for _, e := range entries {
		if !validEntry(e) || counts[e.ID] != 1 {
			continue
		}
		e.Name = strings.TrimSpace(e.Name)
		clean = append(clean, e)
	}
	if len(clean) > 10000 {
		Invalidate(cfg, host, source)
		return
	}
	content, err := json.Marshal(record{version, identity, time.Now().UTC(), clean})
	if err != nil || len(content) > maxBytes {
		Invalidate(cfg, host, source)
		return
	}
	parent := filepath.Dir(path)
	base := filepath.Dir(parent)
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return
	}
	for _, dir := range []string{base, parent} {
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return
		}
		if !privateDir(dir) {
			return
		}
	}
	_ = config.AtomicWrite(path, content, 0600)
}

// Invalidate runs only during normal reads/writes, never during completion.
func Invalidate(cfg config.Config, host, source string) {
	path, _, err := location(cfg, host, source)
	if err != nil || !privateDir(filepath.Dir(path)) || !privateDir(filepath.Dir(filepath.Dir(path))) {
		return
	}
	_ = os.Remove(path)
}
