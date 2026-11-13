// Package execx runs external commands with a timeout and an argument array.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNotFound means the command is not on PATH.
var ErrNotFound = errors.New("command not found in PATH")

// Runner runs one external command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)
}

// OS runs real commands.
type OS struct{}

func (OS) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	full := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch {
	case err == nil:
		return stdout.String(), nil
	case errors.Is(err, exec.ErrNotFound):
		return "", fmt.Errorf("%s: %w", name, ErrNotFound)
	case ctx.Err() == context.DeadlineExceeded:
		return stdout.String(), fmt.Errorf("%s: timed out after %s", full, timeout)
	default:
		return stdout.String(), fmt.Errorf("%s: %w: %s", full, err, strings.TrimSpace(stderr.String()))
	}
}
