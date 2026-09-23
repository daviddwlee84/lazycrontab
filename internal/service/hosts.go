package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
	"github.com/pelletier/go-toml/v2"
)

// HostPlan registers only references to OpenSSH destinations. Connection
// settings and credentials remain owned by OpenSSH.
type HostPlan struct {
	Path     string        `json:"path"`
	Hosts    []config.Host `json:"hosts"`
	Revision string        `json:"revision"`
	before   []byte
	after    []byte
	existed  bool
}

func hostConfigBytes(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, err == nil, err
}

func PlanHosts(c config.Config, hosts []config.Host) (HostPlan, error) {
	if len(hosts) == 0 {
		return HostPlan{}, fmt.Errorf("select at least one SSH alias")
	}
	before, existed, err := hostConfigBytes(c.Path)
	if err != nil {
		return HostPlan{}, err
	}
	fresh := config.Defaults()
	if len(before) > 0 {
		if err := toml.NewDecoder(bytes.NewReader(before)).DisallowUnknownFields().Decode(&fresh); err != nil {
			return HostPlan{}, fmt.Errorf("read current config: %w", err)
		}
	}
	aliases := map[string]bool{}
	ids := map[string]bool{"local": true}
	for _, h := range fresh.Hosts {
		ids[h.ID], aliases[h.SSH] = true, true
	}
	after := append([]byte(nil), before...)
	for _, h := range hosts {
		if ids[h.ID] || aliases[h.SSH] {
			return HostPlan{}, fmt.Errorf("host %q or SSH destination %q is already registered; reload the host list", h.ID, h.SSH)
		}
		ids[h.ID], aliases[h.SSH] = true, true
		fresh.Hosts = append(fresh.Hosts, h)
		encoded, err := toml.Marshal(h)
		if err != nil {
			return HostPlan{}, err
		}
		if len(after) > 0 && after[len(after)-1] != '\n' {
			after = append(after, '\n')
		}
		after = append(after, []byte("\n[[hosts]]\n")...)
		after = append(after, encoded...)
	}
	if err := fresh.Validate(); err != nil {
		return HostPlan{}, err
	}
	digest := sha256.Sum256(before)
	return HostPlan{Path: c.Path, Hosts: append([]config.Host(nil), hosts...), Revision: hex.EncodeToString(digest[:]), before: before, after: after, existed: existed}, nil
}

func (p HostPlan) Description() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Add %d host registration(s) to %s\n\n", len(p.Hosts), p.Path)
	for _, h := range p.Hosts {
		fmt.Fprintf(&b, "  %s → ssh %s\n", h.ID, h.SSH)
	}
	b.WriteString("\nOpenSSH keeps the connection settings and credentials.\nInteractive setup checks each selected host after saving.\nUnavailable hosts stay registered and can be retried later.")
	return b.String()
}

func ApplyHosts(ctx context.Context, p HostPlan) error {
	if p.Path == "" || len(p.Hosts) == 0 || len(p.Revision) != 64 || len(p.after) == 0 {
		return fmt.Errorf("a reviewed host registration plan is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, existed, err := hostConfigBytes(p.Path)
	if err != nil {
		return err
	}
	if existed != p.existed || !bytes.Equal(current, p.before) {
		return fmt.Errorf("host config changed since review; reload and review again")
	}
	if p.existed {
		base, err := config.Base("state")
		if err != nil {
			return err
		}
		backup := filepath.Join(base, "config-backups", fmt.Sprintf("%d-%s.toml", time.Now().UnixNano(), p.Revision[:12]))
		if err := config.AtomicWrite(backup, p.before, 0600); err != nil {
			return fmt.Errorf("back up config: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// As with other native-file operations, an external editor can still race
	// between the revision check and rename. Do not retry a failed read-back.
	if err := config.AtomicWrite(p.Path, p.after, 0600); err != nil {
		return err
	}
	written, err := os.ReadFile(p.Path)
	if err != nil || !bytes.Equal(written, p.after) {
		return fmt.Errorf("host registrations may have been saved; read-back verification failed, inspect config before retrying")
	}
	return nil
}
