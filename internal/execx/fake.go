package execx

import (
	"context"
	"strings"
	"time"
)

// FakeResponse is what Fake returns for one command.
type FakeResponse struct {
	Out string
	Err error
}

// Fake records calls and answers from Responses, keyed by the full command
// joined with spaces. Unknown commands get Default (zero value: "", nil).
type Fake struct {
	Calls     [][]string
	Responses map[string]FakeResponse
	Default   FakeResponse
}

func (f *Fake) Run(_ context.Context, _ time.Duration, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	f.Calls = append(f.Calls, call)
	r, ok := f.Responses[strings.Join(call, " ")]
	if !ok {
		r = f.Default
	}
	return r.Out, r.Err
}
