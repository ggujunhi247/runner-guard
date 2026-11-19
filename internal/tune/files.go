package tune

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var concurrentRe = regexp.MustCompile(`^\s*concurrent\s*=\s*(\d+)\s*(#.*)?$`)

// ReadConcurrent reads the top-level `concurrent = N` from gitlab-runner's
// config.toml (before the first [section]).
func ReadConcurrent(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	i, err := concurrentLine(b)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	m := concurrentRe.FindStringSubmatch(strings.TrimRight(lines(b)[i], "\r"))
	return strconv.Atoi(m[1])
}

func lines(b []byte) []string { return strings.Split(string(b), "\n") }

func concurrentLine(b []byte) (int, error) {
	for i, l := range lines(b) {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			break
		}
		if concurrentRe.MatchString(l) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no top-level concurrent = N line")
}

// SetConcurrent changes only the concurrent line, and only if it still says
// from. The file holds runner tokens: it is never copied anywhere, and the
// write is atomic so gitlab-runner never reloads half a file.
func SetConcurrent(path string, from, to int) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	i, err := concurrentLine(b)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	ls := lines(b)
	old := strings.TrimRight(ls[i], "\r")
	m := concurrentRe.FindStringSubmatch(old)
	if m[1] != strconv.Itoa(from) {
		return fmt.Errorf("%s: concurrent is %s, expected %d; changed by hand?", path, m[1], from)
	}
	cr := strings.TrimPrefix(ls[i], old)
	ls[i] = strings.Replace(old, m[1], strconv.Itoa(to), 1) + cr
	return writeIfUnchanged(path, b, []byte(strings.Join(ls, "\n")))
}

// writeIfUnchanged replaces path with data unless it changed since orig was read.
func writeIfUnchanged(path string, orig, data []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".runner-guard.new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if now, err := os.ReadFile(path); err != nil && !os.IsNotExist(err) || err == nil && !bytes.Equal(now, orig) {
		os.Remove(tmp)
		return fmt.Errorf("%s changed while editing; left alone", path)
	}
	return os.Rename(tmp, path)
}

var wslKeyRe = regexp.MustCompile(`^\s*(memory|swap)\s*=`)

// SetWSL returns .wslconfig text with key (memory or swap) set to gb under
// [wsl2], keeping every other line. It refuses UTF-16 files (PowerShell 5
// writes those) rather than appending ASCII to them; a UTF-8 BOM is dropped.
func SetWSL(text []byte, key string, gb int) ([]byte, error) {
	if bytes.IndexByte(text, 0) >= 0 || bytes.HasPrefix(text, []byte{0xFF, 0xFE}) || bytes.HasPrefix(text, []byte{0xFE, 0xFF}) {
		return nil, fmt.Errorf(".wslconfig is UTF-16; save it as UTF-8 to let runner-guard edit it")
	}
	text = bytes.TrimPrefix(text, []byte{0xEF, 0xBB, 0xBF})
	val := fmt.Sprintf("%s=%dGB", key, gb)
	ls := lines(text)
	if len(text) == 0 {
		ls = nil
	}
	section, at := "", -1
	for i, l := range ls {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			if section == "[wsl2]" && at < 0 {
				at = i // insert before the next section
				break
			}
			section = strings.ToLower(t)
			continue
		}
		if section == "[wsl2]" {
			if m := wslKeyRe.FindStringSubmatch(l); m != nil && strings.EqualFold(m[1], key) {
				ls[i] = val
				return []byte(strings.Join(ls, "\n")), nil
			}
		}
	}
	if at >= 0 {
		ls = append(ls[:at], append([]string{val}, ls[at:]...)...)
		return []byte(strings.Join(ls, "\n")), nil
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	if section != "[wsl2]" {
		ls = append(ls, "[wsl2]")
	}
	ls = append(ls, val, "")
	return []byte(strings.Join(ls, "\n")), nil
}
