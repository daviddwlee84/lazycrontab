// Package hostinventory discovers SSH aliases without evaluating SSH configuration
// or contacting hosts. OpenSSH remains the authority when a host is connected.
package hostinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Candidate struct {
	Alias      string   `json:"alias"`
	Status     string   `json:"status"`
	Selectable bool     `json:"selectable"`
	Sources    []string `json:"sources,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Fleet      []string `json:"fleet,omitempty"`
}

type Inventory struct {
	Source      string      `json:"source"`
	Complete    bool        `json:"complete"`
	Candidates  []Candidate `json:"candidates"`
	Diagnostics []string    `json:"diagnostics,omitempty"`
}

var aliasID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func ValidAlias(alias string) bool {
	return len(alias) <= 255 && aliasID.MatchString(alias) && alias != "local" && alias != "all"
}

// Discover does no network I/O. The optional dev provider uses its supported
// versioned, static inventory command, never its credential or private files.
func Discover(ctx context.Context, source string) (Inventory, error) {
	switch source {
	case "", "ssh":
		home, err := os.UserHomeDir()
		if err != nil {
			return Inventory{}, err
		}
		return ReadSSH(ctx, filepath.Join(home, ".ssh", "config"), home)
	case "dev":
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dev", "ssh", "list", "--json")
		out := &boundedBuffer{limit: 4 << 20}
		stderr := &boundedBuffer{limit: 16 << 10}
		cmd.Stdout, cmd.Stderr = out, stderr
		if err := cmd.Run(); err != nil {
			if ctx.Err() != nil {
				return Inventory{}, ctx.Err()
			}
			return Inventory{}, fmt.Errorf("dev SSH inventory: %w; choose SSH config or enter an alias manually", err)
		}
		if out.overflow {
			return Inventory{}, fmt.Errorf("dev SSH inventory exceeded 4 MiB")
		}
		return DecodeDev(out.Bytes())
	default:
		return Inventory{}, fmt.Errorf("source must be ssh or dev")
	}
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, b.limit-b.buffer.Len())
	if len(p) > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func DecodeDev(raw []byte) (Inventory, error) {
	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Complete      bool   `json:"complete"`
		Aliases       []struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Selectable  bool   `json:"selectable"`
			Definitions []struct {
				Source struct {
					Path string `json:"path"`
					Line int    `json:"line"`
				} `json:"source"`
			} `json:"definitions"`
			Fleet []struct {
				Name string `json:"name"`
			} `json:"fleet"`
		} `json:"aliases"`
		Diagnostics []struct {
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Inventory{}, fmt.Errorf("invalid dev SSH inventory: %w", err)
	}
	if doc.SchemaVersion != 1 || doc.Kind != "ssh_list" {
		return Inventory{}, fmt.Errorf("unsupported dev SSH inventory schema %d (%q); choose SSH config instead", doc.SchemaVersion, doc.Kind)
	}
	out := Inventory{Source: "dev", Complete: doc.Complete, Candidates: []Candidate{}}
	seen := map[string]bool{}
	for _, a := range doc.Aliases {
		if seen[a.Name] {
			return Inventory{}, fmt.Errorf("dev SSH inventory contains duplicate alias %q", a.Name)
		}
		seen[a.Name] = true
		c := Candidate{Alias: a.Name, Status: a.Status, Selectable: a.Selectable && a.Status == "active" && ValidAlias(a.Name)}
		if !c.Selectable {
			c.Reason = "Not a selectable active SSH alias; use an explicit manual registration if needed"
		}
		for _, d := range a.Definitions {
			c.Sources = append(c.Sources, fmt.Sprintf("%s:%d", d.Source.Path, d.Source.Line))
		}
		for _, f := range a.Fleet {
			c.Fleet = append(c.Fleet, f.Name)
		}
		out.Candidates = append(out.Candidates, c)
	}
	for _, d := range doc.Diagnostics {
		out.Diagnostics = append(out.Diagnostics, d.Message)
	}
	sort.Slice(out.Candidates, func(i, j int) bool { return out.Candidates[i].Alias < out.Candidates[j].Alias })
	return out, nil
}

type scanner struct {
	ctx         context.Context
	home        string
	inventory   Inventory
	aliases     map[string]Candidate
	stack       map[string]bool
	files, size int
}

func ReadSSH(ctx context.Context, root, home string) (Inventory, error) {
	s := scanner{ctx: ctx, home: home, inventory: Inventory{Source: "ssh", Complete: true, Candidates: []Candidate{}}, aliases: map[string]Candidate{}, stack: map[string]bool{}}
	if err := s.read(root, false, 0, &sshScope{}); err != nil {
		return Inventory{}, err
	}
	for _, c := range s.aliases {
		s.inventory.Candidates = append(s.inventory.Candidates, c)
	}
	sort.Slice(s.inventory.Candidates, func(i, j int) bool { return s.inventory.Candidates[i].Alias < s.inventory.Candidates[j].Alias })
	return s.inventory, nil
}

func (s *scanner) warning(path string, line int, reason string) {
	s.inventory.Complete = false
	s.inventory.Diagnostics = append(s.inventory.Diagnostics, fmt.Sprintf("%s:%d: %s", path, line, reason))
}

type sshScope struct{ match, hostGuard bool }

func (s *scanner) read(path string, inherited bool, depth int, scope *sshScope) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	if depth > 16 || s.files >= 128 || s.size >= 4<<20 {
		s.warning(path, 0, "SSH Include discovery limit reached")
		return nil
	}
	if s.stack[path] {
		s.warning(path, 0, "SSH Include cycle; skipped")
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		s.warning(path, 0, "SSH configuration is unavailable or not a regular file")
		return nil
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		s.warning(path, 0, "Cannot read SSH configuration")
		return nil
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		s.warning(path, 0, "SSH configuration exceeds 1 MiB; skipped")
		return nil
	}
	s.files++
	s.size += len(raw)
	s.stack[path] = true
	defer delete(s.stack, path)
	for n, line := range strings.Split(string(raw), "\n") {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		fields, err := tokens(line)
		if err != nil {
			s.warning(path, n+1, "Unsupported SSH quoting; remaining declarations are uncertain")
			inherited = true
			continue
		}
		if len(fields) < 2 {
			if len(fields) == 1 && (strings.EqualFold(fields[0], "Host") || strings.EqualFold(fields[0], "Match") || strings.EqualFold(fields[0], "Include")) {
				s.warning(path, n+1, "Missing SSH directive argument; remaining declarations are uncertain")
				inherited = true
			}
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "match":
			scope.match = true
			s.warning(path, n+1, "Match is not evaluated during discovery")
		case "host":
			scope.match = false // Host starts a new conditional block.
			scope.hostGuard = len(fields) != 2 || fields[1] != "*"
			for _, alias := range fields[1:] {
				if strings.ContainsAny(alias, "*?!") {
					continue
				}
				c, exists := s.aliases[alias]
				if !exists {
					c = Candidate{Alias: alias, Status: "candidate", Selectable: ValidAlias(alias)}
				}
				c.Sources = append(c.Sources, fmt.Sprintf("%s:%d", path, n+1))
				if inherited {
					c.Status, c.Selectable, c.Reason = "unknown", false, "Declared inside a conditional Include; enter the alias manually if needed"
				} else if !ValidAlias(alias) {
					c.Status, c.Selectable, c.Reason = "unsupported", false, "Alias cannot be used as a host ID; use manual registration"
				}
				s.aliases[alias] = c
			}
		case "include":
			guarded := inherited || scope.match || scope.hostGuard
			if guarded {
				s.warning(path, n+1, "Conditional Include is scanned as uncertain candidates only")
			}
			for _, pattern := range fields[1:] {
				if strings.ContainsAny(pattern, "$%") || strings.HasPrefix(pattern, "~") && !strings.HasPrefix(pattern, "~/") {
					s.warning(path, n+1, "Dynamic Include path is not evaluated")
					continue
				}
				if strings.HasPrefix(pattern, "~/") {
					pattern = filepath.Join(s.home, pattern[2:])
				} else if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(s.home, ".ssh", pattern)
				}
				paths, err := filepath.Glob(pattern)
				if err != nil {
					s.warning(path, n+1, "Invalid Include pattern")
					continue
				}
				for _, included := range paths {
					if err := s.read(included, guarded, depth+1, scope); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// tokens supports OpenSSH's optional '=' separator, quoted paths and comments.
// It never expands environment variables or invokes a shell.
func tokens(line string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			out = append(out, b.String())
			b.Reset()
			started = false
		}
	}
	for _, r := range line {
		if escaped {
			b.WriteRune(r)
			escaped, started = false, true
			continue
		}
		if r == '\\' {
			escaped, started = true, true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '#' {
			break
		}
		if r == '"' || r == '\'' {
			quote, started = r, true
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '=' && (len(out) == 0 || len(out) == 1 && !started) {
			flush()
			continue
		}
		b.WriteRune(r)
		started = true
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("incomplete quoted token")
	}
	flush()
	return out, nil
}
