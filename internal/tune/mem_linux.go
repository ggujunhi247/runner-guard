package tune

import (
	"context"
	"os"

	"github.com/ggujunhi247/runner-guard/internal/execx"
)

func memText(context.Context, execx.Runner) (string, error) {
	mi, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "", err
	}
	vm, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		return "", err
	}
	return string(mi) + string(vm), nil
}
