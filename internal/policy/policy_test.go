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

// The release public keys are the same set in the updater and both install
// scripts, at least two (one in use, one offline backup), distinct and strong.
func TestReleaseKeysEverywhere(t *testing.T) {
	if len(update.PublicKeys) < 2 {
		t.Fatalf("%d release key(s); keep a backup key so a lost key cannot strand hosts", len(update.PublicKeys))
	}
	sh, ps := read(t, "install.sh"), read(t, "install.ps1")
	seen := map[string]bool{}
	for i, p := range update.PublicKeys {
		blk, _ := pem.Decode([]byte(p))
		if blk == nil {
			t.Fatalf("key %d: no PEM", i)
		}
		k, err := x509.ParsePKIXPublicKey(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		rk := k.(*rsa.PublicKey)
		if rk.N.BitLen() < 3072 || rk.E != 65537 {
			t.Fatalf("key %d weak or odd: %d bits, e=%d", i, rk.N.BitLen(), rk.E)
		}
		mod := base64.StdEncoding.EncodeToString(rk.N.Bytes())
		if seen[mod] {
			t.Fatalf("key %d is a duplicate", i)
		}
		seen[mod] = true
		if !strings.Contains(sh, strings.TrimSpace(p)) {
			t.Errorf("install.sh lacks release key %d", i)
		}
		if !strings.Contains(ps, "'"+mod+"'") {
			t.Errorf("install.ps1 lacks release key %d", i)
		}
	}
	if n := strings.Count(sh, "-----BEGIN PUBLIC KEY-----"); n != len(update.PublicKeys) {
		t.Errorf("install.sh has %d keys, updater has %d", n, len(update.PublicKeys))
	}
	if n := len(regexp.MustCompile(`'[A-Za-z0-9+/]{500,}={0,2}'`).FindAllString(ps, -1)); n != len(update.PublicKeys) {
		t.Errorf("install.ps1 has %d keys, updater has %d", n, len(update.PublicKeys))
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

// Stdlib only: a dependency is a supply-chain path into a root/admin process.
func TestStdlibOnly(t *testing.T) {
	if regexp.MustCompile(`(?m)^\s*(require\b|\w.*\s+v\d)`).MatchString(read(t, "go.mod")) {
		t.Error("go.mod has a require; this module is stdlib only")
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); err == nil {
		t.Error("go.sum exists; this module is stdlib only")
	}
}

// Files are written only through internal/fsx (O_EXCL temp + rename), so a
// pre-planted file or symlink is never followed or truncated.
func TestWritesOnlyThroughFsx(t *testing.T) {
	for path, f := range goFiles(t) {
		if strings.HasPrefix(path, "internal/fsx/") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if pkg, name, ok := selector(n); ok && pkg == "os" && (name == "WriteFile" || name == "Create") {
				t.Errorf("%s uses os.%s; use fsx.WriteFile", path, name)
			}
			return true
		})
	}
}

// Environment variables the program may read. Each is read by name only, so
// the environment of a root/admin process cannot steer it somewhere new.
var allowedEnv = map[string]bool{
	"ProgramData": true, "LOCALAPPDATA": true, "APPDATA": true, "SystemDrive": true,
	"USERDOMAIN": true, "USERNAME": true, "ProgramFiles": true, "RUNNER_GUARD_GITHUB_TOKEN": true,
}

func TestEnvReadsAllowlisted(t *testing.T) {
	for path, f := range goFiles(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if pkg, name, ok := selector(call.Fun); ok && pkg == "os" && (name == "Getenv" || name == "LookupEnv") {
				lit, _ := call.Args[0].(*ast.BasicLit)
				if lit == nil || lit.Kind != token.STRING {
					t.Errorf("%s: os.%s with a non-literal name", path, name)
				} else if s, _ := strconv.Unquote(lit.Value); !allowedEnv[s] {
					t.Errorf("%s: os.%s(%q) is not in the allowlist", path, name, s)
				}
			}
			return true
		})
	}
}

// Network hosts the program may name. A new URL is a new place data can go.
var allowedHosts = map[string]bool{"api.github.com": true}

func TestURLHostsAllowlisted(t *testing.T) {
	for path, f := range goFiles(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			if m := regexp.MustCompile(`https?://([^/\s"'?#:]*)`).FindStringSubmatch(s); m != nil && strings.Contains(s, "://") &&
				(strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) && !allowedHosts[m[1]] {
				t.Errorf("%s: URL host %q is not in the allowlist", path, m[1])
			}
			return true
		})
	}
}

// The workflow-level permissions grant read only; a write is scoped to the
// one job that needs it, so a new job never inherits it.
func TestWorkflowTopPermissionsReadOnly(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	for _, p := range files {
		name := filepath.Base(p)
		in := false
		for i, l := range strings.Split(read(t, filepath.Join(".github", "workflows", name)), "\n") {
			if strings.HasPrefix(l, "permissions:") {
				in = true
				if strings.Contains(l, "write") {
					t.Errorf("%s:%d: top-level permissions grant write", name, i+1)
				}
				continue
			}
			if in && (l == "" || l[0] == ' ') {
				if strings.Contains(l, "write") {
					t.Errorf("%s:%d: top-level permissions grant write; move it into the job", name, i+1)
				}
			} else if in {
				in = false
			}
		}
	}
}

// Auto-merge never covers changes to the security machinery itself: those
// need a person, even from the owner.
func TestAutomergeSkipsSensitivePaths(t *testing.T) {
	wf := read(t, ".github/workflows/automerge.yml")
	for _, p := range []string{`\.github/`, "internal/policy/", "internal/update/", "internal/fsx/", "internal/schedule/",
		"cmd/runner-guard/", "cmd/rgsign/", "scripts/", `install\.sh`, `install\.ps1`, `rg-windows\.ps1`, `go\.mod`, `SECURITY\.md`,
		"previous_filename", "shell: bash", "reviewed", "--remove-label reviewed"} {
		if !strings.Contains(wf, p) {
			t.Errorf("automerge.yml does not guard %s", p)
		}
	}
	if !strings.Contains(wf, "github.actor == github.repository_owner") {
		t.Error("automerge.yml does not require the actor to be the owner or Dependabot")
	}
}

// The leak-scan allowlist names only full commit SHAs, each with a reason, so
// it cannot quietly grow into "allow everything".
func TestLeakScanAllowlist(t *testing.T) {
	for i, l := range strings.Split(strings.TrimSpace(read(t, ".github/leak-scan-allow")), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !regexp.MustCompile(`^[0-9a-f]{40} \S.{10,}$`).MatchString(l) {
			t.Errorf("leak-scan-allow line %d: want '<full sha> <reason>', got %q", i+1, l)
		}
	}
}
