package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

func TestAuthenticationHonorsSharedPolicyAndOwnsOnlyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("FIXTURE_CONTROL_PATH", "/shared/user-master")
	fake := filepath.Join(dir, "ssh")
	os.WriteFile(fake, []byte("#!/bin/sh\nif [ \"$1\" = -G ]; then printf 'controlpath %s\\n' \"$FIXTURE_CONTROL_PATH\"; exit; fi\n"), 0700)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	h := config.Host{ID: "fixture", SSH: "fixture"}
	cmd, _, e := (Native{}).Authentication(context.Background(), h)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "ControlMaster=") {
		t.Fatal("overrode shared master", cmd.Args)
	}
	p, _ := authPath(h)
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("shared auth created fallback state")
	}
	t.Setenv("FIXTURE_CONTROL_PATH", "none")
	cmd, _, e = (Native{}).Authentication(context.Background(), h)
	if e != nil {
		t.Fatal(e)
	}
	record, ok := readAuth(h)
	if !ok {
		t.Fatal("missing owned fallback")
	}
	defer os.RemoveAll(record.Directory)
	if !strings.Contains(strings.Join(cmd.Args, " "), "ControlPersist=600") {
		t.Fatal(cmd.Args)
	}
	read := (Native{}).Command(context.Background(), h, []string{"true"})
	if !strings.Contains(strings.Join(read.Args, " "), "ControlMaster=no") {
		t.Fatal("read did not reuse explicit auth")
	}
	t.Setenv("FIXTURE_CONTROL_PATH", "/new/shared/policy")
	read = (Native{}).Command(context.Background(), h, []string{"true"})
	if strings.Contains(strings.Join(read.Args, " "), record.Directory) {
		t.Fatal("fallback overrode new explicit user policy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd, _, e = (Native{ConnectTimeout: 17}).Authentication(ctx, h)
	if e != nil || !strings.Contains(strings.Join(cmd.Args, " "), "ConnectTimeout=17") {
		t.Fatal("configured timeout was lost", cmd, e)
	}
	cancel()
	if e = cmd.Run(); !errors.Is(e, context.Canceled) {
		t.Fatal("shared-policy authentication ignored cancellation", e)
	}
}
