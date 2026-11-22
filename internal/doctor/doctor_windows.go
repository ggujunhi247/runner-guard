package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/dockerctl"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/schedule"
	"github.com/ggujunhi247/runner-guard/internal/tune"
)

var userID = regexp.MustCompile(`<UserId>([^<]+)</UserId>`)

func osChecks(ctx context.Context, d guard.Deps) []Check {
	var out []Check
	add := func(lvl, name, detail string) { out = append(out, Check{lvl, name, detail}) }

	// WSL: the same reads and the same 10% rule applyWSL uses to keep or roll back a change.
	mem, swap, vmErr := tune.VMMem(ctx, d.R)
	if vmErr != nil {
		add(Fail, "Docker VM memory readable (wsl -d docker-desktop)", errText(vmErr))
	} else {
		add(Pass, "Docker VM memory readable", fmt.Sprintf("memory %.1fGB, swap %.1fGB", mem/1024, swap/1024))
	}
	home, herr := os.UserHomeDir()
	b, rerr := os.ReadFile(filepath.Join(home, ".wslconfig"))
	switch {
	case herr != nil:
		add(Fail, ".wslconfig location", errText(herr))
	case rerr != nil && !os.IsNotExist(rerr):
		add(Fail, ".wslconfig readable", errText(rerr))
	default:
		for _, k := range []struct {
			key string
			vm  float64
		}{{"memory", mem}, {"swap", swap}} {
			v := tune.WSLValue(b, k.key)
			if _, err := tune.SetWSL(b, k.key, int(math.Max(v, 4))); err != nil {
				add(Fail, ".wslconfig editable for "+k.key, errText(err))
				continue
			}
			if v == 0 {
				add(Pass, ".wslconfig "+k.key, "not set (WSL default); editable")
				continue
			}
			got := k.vm / 1024
			if vmErr == nil && math.Abs(got-v) > v*0.1 {
				add(Fail, ".wslconfig "+k.key+" matches the VM",
					fmt.Sprintf("set %gGB, VM reports %.1fGB: every automatic change would be rolled back", v, got))
				continue
			}
			add(Pass, ".wslconfig "+k.key, fmt.Sprintf("%gGB, VM reports %.1fGB", v, got))
		}
	}

	if d.Cfg.RebootRecover {
		if err := guard.CanReboot(ctx, d); err != nil {
			add(Fail, "recovery reboot possible", err.Error()+"; turn on automatic logon or set reboot_recover false")
		} else {
			add(Pass, "recovery reboot possible", "automatic logon is on")
		}
	} else {
		add(Pass, "recovery reboot", "off (reboot_recover false)")
	}

	// The tasks must run as the user whose session has Docker Desktop.
	me, _ := user.Current()
	for _, t := range []string{"runner-guard-run", "runner-guard-update", "runner-guard-sample", dockerctl.DesktopTask} {
		x, err := d.R.Run(ctx, time.Minute, "schtasks", "/Query", "/TN", t, "/XML")
		m := userID.FindStringSubmatch(strings.ReplaceAll(x, "\x00", ""))
		switch {
		case err != nil || m == nil:
			add(Fail, "task "+t, "missing; run install again")
		case me != nil && !strings.EqualFold(m[1], me.Username) && !strings.EqualFold(m[1], me.Uid) &&
			!strings.HasSuffix(strings.ToLower(me.Username), `\`+strings.ToLower(m[1])):
			add(Fail, "task "+t, fmt.Sprintf("runs as %s, not %s: install from the Docker Desktop user's own elevated PowerShell", m[1], me.Username))
		default:
			add(Pass, "task "+t, "runs as "+m[1])
		}
	}

	// After a reboot, autologon must bring Docker Desktop up by itself.
	var s struct{ AutoStart *bool }
	if sb, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "Docker", "settings-store.json")); err == nil && json.Unmarshal(sb, &s) == nil && s.AutoStart != nil {
		if *s.AutoStart {
			add(Pass, "Docker Desktop starts at sign-in", "")
		} else {
			add(Warn, "Docker Desktop starts at sign-in", "off: after a reboot nothing starts Docker until a person does; turn on Start Docker Desktop when you sign in")
		}
	} else {
		add(Warn, "Docker Desktop starts at sign-in", "could not confirm from settings-store.json; check the setting")
	}

	// Stat first: TrustedOwner wraps the Windows error, which os.IsNotExist does not unwrap.
	if _, err := os.Stat(config.Dir()); err != nil {
		add(Warn, "install folder", config.Dir()+" does not exist yet; run install")
	} else if err := schedule.TrustedOwner(config.Dir()); err != nil {
		add(Fail, "install folder owner", err.Error())
	} else {
		add(Pass, "install folder owner", "Administrators/SYSTEM/you")
	}
	return out
}
