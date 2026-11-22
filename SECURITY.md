# Security

## Reporting a vulnerability

Please report privately through GitHub: **Security → Report a vulnerability** on this repository. Do not open a public issue.

## What protects hosts

- Releases are signed with a key kept off GitHub. The updater and both install scripts refuse a release whose `checksums.txt.sig` does not verify. The signer first rebuilds every published file from the tag and signs only on a byte-for-byte match (`scripts/sign-release.sh`).
- Published releases are immutable.
- `main` takes changes only through pull requests that pass every required check: tests on Linux and Windows (including the policy tests in `internal/policy`), govulncheck, actionlint, shellcheck, end-to-end install tests, the leak scan and gitleaks.
- A weekly job scans `main` and the latest release binaries for new Go vulnerabilities.

## Rules enforced as tests (`internal/policy`)

- Commands run only through `internal/execx` (argument lists, timeouts, never a shell).
- No shell or script interpreter is started.
- The runner service is stopped only by `stopRunner`, which records the stop first.
- No truncating reuse of files (`O_TRUNC`), no HTTP client without a timeout, no `InsecureSkipVerify`, no MD5/SHA-1.
- Workflows: actions pinned to commit SHAs, explicit least-privilege `permissions`, no `pull_request_target`, no `${{ }}` inside shell scripts.
- The release public keys (one in use, one offline backup) are the same set in the updater and both install scripts, and CI and the signer hash the same file list.
- Files are written only through `internal/fsx` (fresh temp file + rename; never truncates or follows a symlink).
- Standard library only: `go.mod` has no requirements.
- Environment variables and URL hosts the code may use are allowlisted.
- Workflows grant write permissions only per job, never at the top level.
- Pull requests that touch workflows, policy, update, file-writing, install or signing code never merge automatically.

Parsers that read files or command output (`.wslconfig`, `checksums.txt`, `config.json`, docker output) are fuzzed: the seeds run in every test run, and `security-weekly` fuzzes each for a minute.
