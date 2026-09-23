// Package transport invokes local tools or native OpenSSH with one quoting boundary.
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/daviddwlee84/lazycrontab/internal/config"
)

type Result struct {
	Truncated bool
	Stdout    []byte
	Stderr    string
	Code      int
}

var ErrOutputLimit = errors.New("command output exceeded 16 MiB; refusing incomplete data")

type Runner interface {
	Run(context.Context, config.Host, []string, []byte) (Result, error)
}
type Native struct{ ConnectTimeout int }
type limitBuffer struct {
	mu       sync.Mutex
	b        bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - b.b.Len()
	if remaining < len(p) {
		b.overflow = true
		p = p[:max(0, remaining)]
	}
	_, _ = b.b.Write(p)
	return n, nil
}
func (b *limitBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func Join(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = Quote(a)
	}
	return strings.Join(out, " ")
}
func Path(p string) string {
	if p == "~" {
		return `"$HOME"`
	}
	if strings.HasPrefix(p, "~/") {
		return `"$HOME"/` + Quote(p[2:])
	}
	return Quote(p)
}
func (n Native) Command(ctx context.Context, h config.Host, argv []string) *exec.Cmd {
	if h.SSH == "" {
		return exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	timeout := n.ConnectTimeout
	if timeout < 1 {
		timeout = 10
	}
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(timeout)}
	args = append(args, fallbackArgs(ctx, h)...)
	args = append(args, "--", h.SSH, Join(argv))
	return exec.CommandContext(ctx, "ssh", args...)
}
func (n Native) Run(ctx context.Context, h config.Host, argv []string, input []byte) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("empty command")
	}
	cmd := n.Command(ctx, h, argv)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Stdin = bytes.NewReader(input)
	out := &limitBuffer{limit: 16 << 20}
	stderr := &limitBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	r := Result{Stdout: out.Bytes(), Stderr: string(stderr.Bytes()), Truncated: out.overflow || stderr.overflow}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.Code = exit.ExitCode()
	} else if err != nil {
		r.Code = -1
	}
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	if out.overflow {
		return r, ErrOutputLimit
	}
	if err != nil {
		return r, fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(r.Stderr))
	}
	return r, nil
}
func (n Native) Authenticate(h config.Host) *exec.Cmd {
	return exec.Command("ssh", "-o", "ConnectTimeout=10", "--", h.SSH, "true")
}
func CopyBounded(dst io.Writer, src io.Reader, n int64) (bool, error) {
	written, err := io.Copy(dst, io.LimitReader(src, n))
	if err != nil {
		return false, err
	}
	if written < n {
		return false, nil
	}
	var b [1]byte
	k, _ := src.Read(b[:])
	return k > 0, nil
}
func Interactive(cmd *exec.Cmd) error {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
