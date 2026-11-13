package disk

import (
	"context"
	"errors"

	"github.com/ggujunhi247/runner-guard/internal/config"
	"github.com/ggujunhi247/runner-guard/internal/execx"
)

// Reclaim is Docker Desktop only; a Linux filesystem frees space directly.
func Reclaim(context.Context, execx.Runner, config.Config, func() error) (string, error) {
	return "", errors.ErrUnsupported
}
