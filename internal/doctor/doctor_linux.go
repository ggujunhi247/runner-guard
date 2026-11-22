package doctor

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/guard"
	"github.com/ggujunhi247/runner-guard/internal/schedule"
)

func osChecks(ctx context.Context, d guard.Deps) []Check {
	var out []Check
	add := func(lvl, name, detail string) { out = append(out, Check{lvl, name, detail}) }
	for _, t := range []string{"run", "update", "sample"} {
		if _, err := d.R.Run(ctx, time.Minute, "systemctl", "is-enabled", "runner-guard-"+t+".timer"); err != nil {
			add(Fail, "timer runner-guard-"+t, "not enabled; run install again")
		} else {
			add(Pass, "timer runner-guard-"+t, "enabled")
		}
	}
	if d.Cfg.RebootRecover {
		add(Warn, "recovery reboot", "on: fine for a CI-only host, not for one that also runs production")
	} else {
		add(Pass, "recovery reboot", "off (reboot_recover false)")
	}
	for _, dir := range []string{config.Dir(), filepath.Dir(config.File())} {
		if _, err := os.Stat(dir); err != nil {
			add(Warn, dir, "does not exist yet; run install")
		} else if err := schedule.TrustedOwner(dir); err != nil {
			add(Fail, "owner of "+dir, err.Error())
		} else {
			add(Pass, "owner of "+dir, "root only")
		}
	}
	return out
}
