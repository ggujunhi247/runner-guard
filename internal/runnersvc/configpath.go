package runnersvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// ConfigPath returns the config.toml the runner service really uses: the
// configured path when that file exists, else the --config argument of the
// service command line, else the file next to the service's working
// directory or binary. "" when nothing exists.
func ConfigPath(ctx context.Context, r execx.Runner, name, configured string) string {
	if configured != "" && exists(configured) {
		return configured
	}
	cmdline := serviceCommand(ctx, r, name)
	for _, p := range []string{
		flagValue(cmdline, "--config", "-c"),
		join(flagValue(cmdline, "--working-directory", "-d"), "config.toml"),
		join(filepath.Dir(firstArg(cmdline)), "config.toml"),
	} {
		if p != "" && exists(p) {
			return p
		}
	}
	return ""
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func join(dir, f string) string {
	if dir == "" || dir == "." {
		return ""
	}
	return filepath.Join(dir, f)
}

// args splits a command line on spaces, keeping "quoted parts" together.
func args(cmdline string) []string {
	var out []string
	var cur strings.Builder
	quoted, any := false, false
	for _, c := range cmdline {
		switch {
		case c == '"':
			quoted, any = !quoted, true
		case (c == ' ' || c == '\t' || c == ';') && !quoted:
			if any {
				out = append(out, cur.String())
			}
			cur.Reset()
			any = false
		default:
			cur.WriteRune(c)
			any = true
		}
	}
	if any {
		out = append(out, cur.String())
	}
	return out
}

func flagValue(cmdline string, names ...string) string {
	a := args(cmdline)
	for i, s := range a {
		for _, n := range names {
			if s == n && i+1 < len(a) {
				return a[i+1]
			}
			if v, ok := strings.CutPrefix(s, n+"="); ok {
				return v
			}
		}
	}
	return ""
}

func firstArg(cmdline string) string {
	if a := args(cmdline); len(a) > 0 {
		return a[0]
	}
	return ""
}
