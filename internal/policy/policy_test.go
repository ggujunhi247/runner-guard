// Package policy holds repo-wide security rules as tests, so a change that
// breaks one fails CI instead of relying on someone remembering it. Each rule
// says why it exists; change a rule only together with its reason.
package policy

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ggujunhi247/runner-guard/internal/update"
)

const root = "../.."

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// goFiles parses every non-test Go file in the module.
func goFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".worktrees" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = f
		return nil
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("walk: %v (%d files)", err, len(out))
	}
	return out
}

// forEachCall visits calls and selector uses with the enclosing function name.
func walkFuncs(f *ast.File, fn func(funcName string, n ast.Node)) {
	for _, decl := range f.Decls {
		name := ""
		if fd, ok := decl.(*ast.FuncDecl); ok {
			name = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if n != nil {
				fn(name, n)
			}
			return true
		})
	}
}

func selector(n ast.Node) (pkg, name string, ok bool) {
	s, ok := n.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	id, ok := s.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	return id.Name, s.Sel.Name, true
}

// Commands run only through internal/execx: argument lists, a timeout, no shell.
func TestCommandsOnlyThroughExecx(t *testing.T) {
	for path, f := range goFiles(t) {
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "os/exec" && path != "internal/execx/execx.go" {
				t.Errorf("%s imports os/exec; run commands through execx.Runner", path)
			}
		}
	}
}

// No shell or script interpreter is ever started: a value from a config file
// or command output must never be parsed as code by a root/admin process.
func TestNoShellInterpreters(t *testing.T) {
	shells := map[string]bool{"sh": true, "bash": true, "cmd": true, "cmd.exe": true,
		"powershell": true, "powershell.exe": true, "pwsh": true, "wscript": true, "cscript": true, "mshta": true}
	for path, f := range goFiles(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && shells[strings.ToLower(s)] {
					t.Errorf("%s: string %q looks like a shell being run", path, s)
				}
			}
			return true
		})
	}
}

// Stopping the runner is recorded in state first, and stopRunner is the only
// place that does that (docs/solutions/logic-errors/stopping-the-runner-...).
func TestRunnerStoppedOnlyByStopRunner(t *testing.T) {
	for path, f := range goFiles(t) {
		walkFuncs(f, func(fn string, n ast.Node) {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return
			}
			if pkg, name, ok := selector(call.Fun); ok && pkg == "runnersvc" && name == "Stop" &&
				(path != "internal/guard/guard.go" || fn != "stopRunner") {
				t.Errorf("%s: %s calls runnersvc.Stop; use Deps.stopRunner", path, fn)
			}
		})
	}
}

// Forbidden APIs, each with its reason.
func TestForbiddenAPIs(t *testing.T) {
	forbidden := map[string]string{
		"os.O_TRUNC":         "truncating reuses a file someone else may have created (and still own); remove it and create with O_EXCL",
		"http.DefaultClient": "no timeout; use an http.Client with Timeout",
		"http.Get":           "uses http.DefaultClient (no timeout)",
		"http.Post":          "uses http.DefaultClient (no timeout)",
		"http.Head":          "uses http.DefaultClient (no timeout)",
		"rand.Seed":          "math/rand is not for anything security related; use crypto/rand",
		"md5.New":            "broken hash",
		"md5.Sum":            "broken hash",
		"sha1.New":           "broken hash",
		"sha1.Sum":           "broken hash",
	}
	for path, f := range goFiles(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			if pkg, name, ok := selector(n); ok {
				if why := forbidden[pkg+"."+name]; why != "" {
					t.Errorf("%s uses %s.%s: %s", path, pkg, name, why)
				}
			}
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "InsecureSkipVerify" {
					t.Errorf("%s sets InsecureSkipVerify", path)
				}
			}
			return true
		})
	}
}

var pinnedUses = regexp.MustCompile(`^\s*(- )?uses:\s*[\w.-]+/[\w./-]+@[0-9a-f]{40}(\s+#.*)?$`)

// Workflows: actions pinned to a commit, least privilege, no untrusted
// triggers, and no ${{ }} expanded inside a shell script (script injection:
// pass values through env: instead).
func TestWorkflows(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if len(files) == 0 {
		t.Fatal("no workflows found")
	}
	for _, p := range files {
		name := filepath.Base(p)
		text := read(t, filepath.Join(".github", "workflows", name))
		if !regexp.MustCompile(`(?m)^permissions:`).MatchString(text) {
			t.Errorf("%s: no top-level permissions: block", name)
		}
		for _, bad := range []string{"pull_request_target", "workflow_run", "secrets.GITHUB_TOKEN", "permissions: write-all"} {
			if strings.Contains(text, bad) {
				t.Errorf("%s: uses %s", name, bad)
			}
		}
		lines := strings.Split(text, "\n")
		runIndent := -1
		for i, l := range lines {
			indent := len(l) - len(strings.TrimLeft(l, " "))
			trimmed := strings.TrimSpace(l)
			if runIndent >= 0 {
				if trimmed == "" || indent > runIndent {
					if strings.Contains(l, "${{") {
						t.Errorf("%s:%d: ${{ }} inside a run script; pass it via env:", name, i+1)
					}
					continue
				}
				runIndent = -1
			}
			if strings.Contains(trimmed, "uses:") && !pinnedUses.MatchString(l) {
				t.Errorf("%s:%d: action not pinned to a commit SHA: %s", name, i+1, trimmed)
			}
			if m := regexp.MustCompile(`^(- )?run:\s*(.*)$`).FindStringSubmatch(trimmed); m != nil {
				if strings.Contains(m[2], "${{") {
					t.Errorf("%s:%d: ${{ }} inside a run command; pass it via env:", name, i+1)
				}
				if m[2] == "|" || m[2] == ">" || strings.HasPrefix(m[2], "|") || strings.HasPrefix(m[2], ">") {
					runIndent = indent
					if strings.HasPrefix(trimmed, "- ") {
						runIndent += 2
					}
				}
			}
		}
	}
}

// The release public key is the same in the updater and both install scripts.
func TestReleaseKeyEverywhere(t *testing.T) {
	blk, _ := pem.Decode([]byte(update.PublicKey))
	if blk == nil {
		t.Fatal("no PEM in update.PublicKey")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	rk := k.(*rsa.PublicKey)
	if rk.N.BitLen() < 3072 || rk.E != 65537 {
		t.Fatalf("weak or odd release key: %d bits, e=%d", rk.N.BitLen(), rk.E)
	}
	if sh := read(t, "install.sh"); !strings.Contains(sh, strings.TrimSpace(update.PublicKey)) {
		t.Error("install.sh has a different release key than internal/update")
	}
	ps := read(t, "install.ps1")
	if !strings.Contains(ps, "FromBase64String('"+base64.StdEncoding.EncodeToString(rk.N.Bytes())+"')") ||
		!strings.Contains(ps, "FromBase64String('AQAB')") {
		t.Error("install.ps1 has a different release key than internal/update")
	}
}

// Every published file is in checksums.txt, and CI and the signing script
// hash the same list (the signature covers only what is listed).
func TestSignedFileListsAgree(t *testing.T) {
	want := []string{"runner-guard_linux_amd64", "runner-guard_windows_amd64.exe", "rg-windows.ps1", "runner-guard-windows-guide.md"}
	wf := read(t, ".github/workflows/release.yml")
	m := regexp.MustCompile(`sha256sum ([^>]+)>\s*checksums.txt`).FindStringSubmatch(wf)
	if m == nil || strings.Join(strings.Fields(m[1]), " ") != strings.Join(want, " ") {
		t.Errorf("release.yml hashes %q, want %q", m, want)
	}
	sh := read(t, "scripts/sign-release.sh")
	m = regexp.MustCompile(`for f in ([^;]+); do\n\s*printf`).FindStringSubmatch(sh)
	if m == nil || strings.Join(strings.Fields(m[1]), " ") != strings.Join(want, " ") {
		t.Errorf("sign-release.sh hashes %q, want %q", m, want)
	}
	up := regexp.MustCompile(`gh release create[^\n]*`).FindString(wf)
	for _, f := range append(want, "checksums.txt") {
		name := f
		if strings.HasPrefix(f, "runner-guard_") {
			name = "runner-guard_*"
		}
		if !strings.Contains(up, name) {
			t.Errorf("release.yml does not publish %s", f)
		}
	}
}
