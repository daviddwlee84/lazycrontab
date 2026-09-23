package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

type authRecord struct {
	Directory string `json:"directory"`
}

func authPath(h config.Host) (string, error) {
	base, e := config.Base("cache")
	sum := sha256.Sum256([]byte(h.SSH))
	return filepath.Join(base, "ssh", hex.EncodeToString(sum[:])+".json"), e
}
func validAuthDir(path string) bool {
	if filepath.Dir(path) != "/tmp" || !strings.HasPrefix(filepath.Base(path), "lct-"+strconv.Itoa(os.Getuid())+"-") {
		return false
	}
	st, e := os.Lstat(path)
	if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return ok && sys.Uid == uint32(os.Getuid())
}
func readAuth(h config.Host) (authRecord, bool) {
	p, e := authPath(h)
	if e != nil {
		return authRecord{}, false
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return authRecord{}, false
	}
	var record authRecord
	if json.Unmarshal(b, &record) != nil || !validAuthDir(record.Directory) {
		return authRecord{}, false
	}
	return record, true
}
func configuredControlPath(ctx context.Context, h config.Host) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, e := exec.CommandContext(ctx, "ssh", "-G", "--", h.SSH).Output()
	if e != nil {
		return "", e
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok && strings.EqualFold(key, "controlpath") {
			return strings.TrimSpace(value), nil
		}
	}
	return "none", nil
}

// fallbackArgs only reuses an explicitly created app master. It does not create
// directories, authenticate, or override a configured OpenSSH ControlPath.
func fallbackArgs(ctx context.Context, h config.Host) []string {
	record, ok := readAuth(h)
	if !ok {
		return nil
	}
	path, e := configuredControlPath(ctx, h)
	if e != nil || path != "none" && path != "" {
		return nil
	}
	return []string{"-o", "ControlMaster=no", "-o", "ControlPath=" + filepath.Join(record.Directory, "%C")}
}

// Authentication prepares a native prompt. Passwords never enter application
// fields. A fallback master expires after ten idle minutes and may be reused by
// the dashboard's separate CLI handoffs.
func (n Native) Authentication(ctx context.Context, h config.Host) (*exec.Cmd, string, error) {
	path, e := configuredControlPath(ctx, h)
	if e != nil {
		return nil, "", fmt.Errorf("inspect SSH configuration: %w", e)
	}
	if path != "" && path != "none" {
		return n.Authenticate(h), "Using the configured OpenSSH sharing policy.", nil
	}
	record, ok := readAuth(h)
	if !ok {
		dir, e := os.MkdirTemp("/tmp", "lct-"+strconv.Itoa(os.Getuid())+"-")
		if e != nil {
			return nil, "", e
		}
		record.Directory = dir
	}
	marker, e := authPath(h)
	if e != nil {
		return nil, "", e
	}
	b, _ := json.Marshal(record)
	if e = config.AtomicWrite(marker, b, 0600); e != nil {
		return nil, "", e
	}
	args := []string{"-o", "ConnectTimeout=10", "-o", "ControlMaster=auto", "-o", "ControlPersist=600", "-o", "ControlPath=" + filepath.Join(record.Directory, "%C"), "--", h.SSH, "true"}
	return exec.CommandContext(ctx, "ssh", args...), "App-owned SSH connection expires after ten idle minutes; no password is stored.", nil
}
