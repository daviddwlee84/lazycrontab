package transport

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

func TestRemoteQuotingRoundTrip(t *testing.T) {
	want := "a ' quote $(printf UNEXPECTED)\nline %s"
	script := Join([]string{"printf", "%s", want})
	out, e := exec.Command("sh", "-c", script).Output()
	if e != nil || string(out) != want {
		t.Fatalf("%q %v", out, e)
	}
	cmd := (Native{}).Command(context.Background(), config.Host{SSH: "lab"}, []string{"sh", "-c", "echo hello"})
	if !strings.Contains(strings.Join(cmd.Args, " "), "BatchMode=yes") || cmd.Args[len(cmd.Args)-2] != "lab" {
		t.Fatal(cmd.Args)
	}
}
func TestCancelDoesNotWaitForChildPipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := (Native{}).Run(ctx, config.Host{ID: "local"}, []string{"sh", "-c", "sleep 30 & wait"}, nil)
	if e == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation failed: %v %v", time.Since(start), e)
	}
}
