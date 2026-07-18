# GoSCP Security Requirements

Status: DRAFT — Pre-implementation contract for T3 (SSH/SFTP implementation).

This document is the security contract. Every requirement here MUST be satisfied before
the SSH/SFTP implementation (T3) is considered complete. Deviations require an explicit
architectural decision record (ADR) and sign-off.

---

## 1. Host Key Verification Policy

### Requirement

Host key verification is MANDATORY. GoSCP MUST fail closed on any unknown or mismatched
host key. There is no user-facing flag to disable this.

### Implementation Pattern

Use `golang.org/x/crypto/ssh/knownhosts` — the standard library callback, not a custom one.

```go
// pkg/ssh/known_hosts.go

import (
    "golang.org/x/crypto/ssh"
    "golang.org/x/crypto/ssh/knownhosts"
    "os"
    "path/filepath"
)

func KnownHostsCallback(paths ...string) (ssh.HostKeyCallback, error) {
    if len(paths) == 0 {
        home, err := os.UserHomeDir()
        if err != nil {
            return nil, fmt.Errorf("known_hosts: resolve home: %w", err)
        }
        paths = []string{filepath.Join(home, ".ssh", "known_hosts")}
    }
    cb, err := knownhosts.New(paths...)
    if err != nil {
        return nil, fmt.Errorf("known_hosts: load %v: %w", paths, err)
    }
    return cb, nil
}
```

Wire into `ssh.ClientConfig`:

```go
cb, err := ssh.KnownHostsCallback()
if err != nil {
    return nil, err // caller propagates — no silent fallback
}
cfg := &ssh.ClientConfig{
    User:            user,
    Auth:            authMethods,
    HostKeyCallback: cb,
    Timeout:         30 * time.Second,
}
```

### Behaviour on Key Events

| Event                          | Required Behaviour                                              |
|--------------------------------|-----------------------------------------------------------------|
| Host key not in known_hosts    | Return error: "host key not found in known_hosts — add manually with ssh-keyscan" |
| Host key present but mismatched| Return error: "host key MISMATCH — possible MITM attack; refusing connection"     |
| Host key present and matches   | Proceed                                                        |

Both error cases MUST be non-retryable (classify in `internal/errs` as `ErrHostKeyRejected`).
Do NOT auto-accept or auto-update known_hosts. Do NOT prompt the user interactively.

### Linter Enforcement

Add a `nogo` or `golangci-lint` rule to ban `ssh.InsecureIgnoreHostKey` outside test files.

In `.golangci.yml`:

```yaml
linters-settings:
  gocritic:
    disabled-checks: []

issues:
  exclude-rules:
    # Allow InsecureIgnoreHostKey only in test files
    - path: "_test\\.go"
      text: "InsecureIgnoreHostKey"
  # Fail build if InsecureIgnoreHostKey appears in production code
```

Additionally, add a `grep`-based CI gate:

```bash
# In CI (Makefile or GitHub Actions)
check-no-insecure-host-key:
    @if grep -r "InsecureIgnoreHostKey" --include="*.go" \
        $(shell find . -name "*.go" ! -name "*_test.go"); then \
        echo "FATAL: InsecureIgnoreHostKey found in production code"; exit 1; fi
```

This check runs in CI before any build step.

---

## 2. Authentication Priority Order

### Required Order

1. SSH agent via `SSH_AUTH_SOCK` (if socket exists and is reachable)
2. Explicit identity file from `-i` / `--identity` flag
3. System default keys: `~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, `~/.ssh/id_ecdsa` (tried in this order)
4. If none succeed: fail with a clear, explicit error — no silent fallback, no password prompt

### Implementation Pattern

```go
// pkg/ssh/ssh_dialer.go

func buildAuthMethods(cfg DialConfig) ([]ssh.AuthMethod, error) {
    var methods []ssh.AuthMethod

    // 1. SSH agent
    if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
        conn, err := net.Dial("unix", sock)
        if err == nil {
            ag := agent.NewClient(conn)
            methods = append(methods, ssh.PublicKeysCallback(ag.Signers))
        }
        // If socket exists but dial fails: log warning, continue — do not abort
    }

    // 2. Explicit identity file
    if cfg.IdentityFile != "" {
        signer, err := loadPrivateKey(cfg.IdentityFile)
        if err != nil {
            return nil, fmt.Errorf("identity file %q: %w", cfg.IdentityFile, err)
        }
        methods = append(methods, ssh.PublicKeys(signer))
    }

    // 3. System defaults (only if no explicit identity given)
    if cfg.IdentityFile == "" {
        defaults := []string{"id_ed25519", "id_rsa", "id_ecdsa"}
        home, _ := os.UserHomeDir()
        for _, name := range defaults {
            path := filepath.Join(home, ".ssh", name)
            if signer, err := loadPrivateKey(path); err == nil {
                methods = append(methods, ssh.PublicKeys(signer))
            }
        }
    }

    if len(methods) == 0 {
        return nil, fmt.Errorf("no authentication methods available: " +
            "set SSH_AUTH_SOCK, provide -i flag, or ensure ~/.ssh/id_* keys exist")
    }
    return methods, nil
}
```

`loadPrivateKey` MUST:
- Reject world-readable key files (mode & 0o044 != 0) — log error and skip
- Support passphrase-protected keys: prompt once via `golang.org/x/term`, do NOT store in memory beyond single use
- Return a wrapped error naming the file path on failure

### Auth Error Surfacing

- Auth failures from the SSH handshake propagate as non-retryable errors.
- The error message MUST name the attempted auth method: "publickey auth failed for user 'root' at host 'bastion.example.com'".
- Do NOT log or print private key material, passphrase, or agent signer details.
- Password authentication is NOT supported. Do not include `ssh.Password(...)` or `ssh.PasswordCallback(...)` as auth methods in production code.

---

## 3. ProxyJump Threat Surface

### Overview

ProxyJump creates a chain of SSH connections. Each hop is a separate authentication event.
The threat surface expands with each hop: credential exposure, hop-to-hop MITM, and agent
forwarding misuse.

### Credential Forwarding Rules

| Rule | Detail |
|------|--------|
| Authenticate to EACH hop independently | Do not reuse credentials across hops |
| Known_hosts checked at EACH hop | The callback for hop B is not the same as hop A |
| User identity from CLIENT only | The client's own key/agent signs each hop — no credential is forwarded |
| ProxyJump user may differ | Allow `user@jumphost` syntax; user on final target is set separately |

### Implementation Pattern (Multi-Hop)

```go
// pkg/ssh/ssh_dialer.go

func dialViaJump(ctx context.Context, jumps []JumpHost, target DialTarget, cfg *ssh.ClientConfig) (*ssh.Client, error) {
    var current *ssh.Client
    for i, jump := range jumps {
        jumpCfg := buildJumpConfig(jump) // independent auth for this hop
        var err error
        if current == nil {
            current, err = ssh.Dial("tcp", jump.Addr, jumpCfg)
        } else {
            nc, err2 := current.DialContext(ctx, "tcp", jump.Addr)
            if err2 != nil {
                return nil, fmt.Errorf("proxyjump hop %d dial: %w", i, err2)
            }
            conn, chans, reqs, err2 := ssh.NewClientConn(nc, jump.Addr, jumpCfg)
            if err2 != nil {
                return nil, fmt.Errorf("proxyjump hop %d handshake: %w", i, err2)
            }
            current = ssh.NewClient(conn, chans, reqs)
        }
        if err != nil {
            return nil, fmt.Errorf("proxyjump hop %d: %w", i, err)
        }
    }
    // Final target
    nc, err := current.DialContext(ctx, "tcp", target.Addr)
    if err != nil {
        return nil, fmt.Errorf("proxyjump final dial: %w", err)
    }
    conn, chans, reqs, err := ssh.NewClientConn(nc, target.Addr, cfg)
    if err != nil {
        return nil, fmt.Errorf("proxyjump final handshake: %w", err)
    }
    return ssh.NewClient(conn, chans, reqs), nil
}
```

### What NOT To Do

| Prohibited | Reason |
|------------|--------|
| `ssh.RequestAgentForwarding(session)` | Exposes the local agent to ALL hosts in the chain — a compromised jump host gains access to every key the agent holds |
| Sharing a single `*ssh.Client` across hops without re-authenticating | The session is tied to the first hop's auth; subsequent hops must independently authenticate |
| Passing the client's private key bytes over an SSH channel to a jump host | Credentials never leave the client process |
| Using `ForwardAgent: yes` equivalent | Never. If the remote needs to reach a third host, a separate goscp invocation is the correct model |

The distinction: `agent auth` means the CLIENT authenticates using its local agent's signers.
`agent forwarding` means the remote host gets to USE the client's agent — this is categorically prohibited.

---

## 4. Acceptable Cipher / MAC / KEX Algorithms

### Baseline

GoSCP uses `golang.org/x/crypto/ssh` which negotiates from its built-in defaults. As of
the current library version the defaults include modern and legacy algorithms. GoSCP MUST
restrict to the safe subset below.

### Approved Algorithms

**Key Exchange (KEX):**
- `curve25519-sha256`
- `curve25519-sha256@libssh.org`
- `ecdh-sha2-nistp256`
- `ecdh-sha2-nistp384`
- `ecdh-sha2-nistp521`

**Ciphers:**
- `chacha20-poly1305@openssh.com`
- `aes256-gcm@openssh.com`
- `aes128-gcm@openssh.com`
- `aes256-ctr`
- `aes192-ctr`
- `aes128-ctr`

**MACs:**
- `hmac-sha2-256-etm@openssh.com`
- `hmac-sha2-512-etm@openssh.com`
- `hmac-sha2-256`
- `hmac-sha2-512`

### Explicitly Excluded

| Algorithm | Reason |
|-----------|--------|
| `arcfour`, `arcfour128`, `arcfour256` | RC4 — broken |
| `aes128-cbc`, `aes192-cbc`, `aes256-cbc`, `3des-cbc` | CBC mode — BEAST/Lucky13 vulnerable |
| `hmac-sha1`, `hmac-md5` | Weak MAC — collision risk |
| `diffie-hellman-group1-sha1`, `diffie-hellman-group14-sha1` | Weak KEX — Logjam |
| `none` (cipher) | Plaintext — prohibited |

### Implementation

```go
cfg := &ssh.ClientConfig{
    // ...
    Config: ssh.Config{
        KeyExchanges: []string{
            "curve25519-sha256",
            "curve25519-sha256@libssh.org",
            "ecdh-sha2-nistp256",
            "ecdh-sha2-nistp384",
            "ecdh-sha2-nistp521",
        },
        Ciphers: []string{
            "chacha20-poly1305@openssh.com",
            "aes256-gcm@openssh.com",
            "aes128-gcm@openssh.com",
            "aes256-ctr",
            "aes192-ctr",
            "aes128-ctr",
        },
        MACs: []string{
            "hmac-sha2-256-etm@openssh.com",
            "hmac-sha2-512-etm@openssh.com",
            "hmac-sha2-256",
            "hmac-sha2-512",
        },
    },
}
```

If the server does not support any of the approved algorithms, the connection MUST fail.
Do not fall back to excluded algorithms.

---

## 5. Path Traversal Mitigations

### Threat

A malicious or compromised remote SFTP server can return crafted `Stat` responses with
paths containing `..`, symlinks pointing outside the intended tree, or absolute paths
that escape the intended destination directory.

### Rules for Destination Path Sanitization

1. All remote path strings received via SFTP (stat names, readdir entries) MUST be
   cleaned with `path.Clean` before use.
2. After cleaning, validate the resolved path is within the expected destination root:

```go
// pkg/resolve/default_resolver.go

func safeDstPath(root, remoteName string) (string, error) {
    cleaned := path.Clean(remoteName) // path (not filepath) for remote POSIX paths
    if strings.HasPrefix(cleaned, "..") || strings.Contains(cleaned, "/../") {
        return "", fmt.Errorf("path traversal rejected: %q", remoteName)
    }
    // For local destinations only: ensure the joined path stays within root
    joined := filepath.Join(root, cleaned)
    absRoot, _ := filepath.Abs(root)
    absJoined, _ := filepath.Abs(joined)
    if !strings.HasPrefix(absJoined, absRoot+string(os.PathSeparator)) && absJoined != absRoot {
        return "", fmt.Errorf("destination path escapes root %q: resolved to %q", root, absJoined)
    }
    return joined, nil
}
```

3. File names from remote MUST NOT be used as shell arguments or passed to `exec.Command`.
   SFTP I/O only — no shell execution of remote-supplied names.

4. Destination path length MUST be validated. Reject paths exceeding 4096 bytes (NAME_MAX
   enforcement is OS-level but an early check reduces attack surface).

### Symlink Policy

| Target type | Symlink on source (remote) | Symlink on destination (local or remote) |
|-------------|---------------------------|------------------------------------------|
| Read (download) | Follow, but check resolved path stays within source tree | N/A |
| Write (upload)  | Source stat is the file data — OK to follow for reads | REJECT — do not write through symlinks on destination |

Write-through symlink rejection implementation:

```go
// Before opening destination for write:
dstInfo, err := dstFS.Lstat(dstPath) // Lstat, NOT Stat
if err == nil && dstInfo.Mode()&os.ModeSymlink != 0 {
    return fmt.Errorf("destination %q is a symlink — refusing to write through symlink", dstPath)
}
```

For remote destinations via SFTP: the `github.com/pkg/sftp` client exposes `Lstat`.
Use it before any `Create` call on the destination path.

---

## 6. Checkpoint File Trust Model

### Context

`pkg/checkpoint/file_checkpointer.go` reads and writes checkpoint JSON to local disk
(default: `~/.local/share/goscp/checkpoints/<id>.json` or `--checkpoint-dir`).

### Trust Level: UNTRUSTED INPUT

Checkpoint files on disk are treated as UNTRUSTED. An attacker with write access to the
checkpoint directory can craft a malicious checkpoint to influence transfer behaviour.

### Required Mitigations

1. **Schema validation on load.** After JSON unmarshalling, validate all fields:
   - `ID` and `TransferID`: non-empty, alphanumeric + hyphens only (reject path components)
   - `Files[*].Source.Path` and `Files[*].Dest.Path`: apply path sanitization (Section 5 rules)
   - `Chunks[*].Offset` and `Chunks[*].Length`: must be >= 0 and <= file size (reject negative/overflow)
   - `Meta` map keys: reject keys longer than 256 chars; reject keys containing null bytes

2. **No code execution from checkpoint.** Checkpoint data MUST NOT be used to construct
   shell commands, dynamic imports, or `exec.Command` arguments.

3. **File permissions on write.** Checkpoint files MUST be written with mode `0600`
   (owner read/write only). Reject loading checkpoint files with mode wider than `0600`.

```go
// On save:
if err := os.WriteFile(path, data, 0600); err != nil { ... }

// On load:
info, err := os.Stat(path)
if err != nil { return ... }
if info.Mode().Perm() & 0o177 != 0 {
    return CheckpointState{}, fmt.Errorf("checkpoint %q has unsafe permissions %o — expected 0600", path, info.Mode().Perm())
}
```

4. **Checkpoint directory permissions.** If creating the checkpoint directory, use mode
   `0700`. Warn (do not abort) if an existing directory has wider permissions.

5. **No TOCTOU on load.** Open the file and stat in a single `os.Open` + `file.Stat()`
   sequence rather than separate `os.Stat` then `os.Open`.

---

## 7. Rate Limiter Bypass Risk

### Threat Model

The `RateLimiter` in `pkg/ratelimit` wraps `golang.org/x/time/rate` (token bucket).
A malicious or slow remote can influence the effective transfer rate in these ways:

| Attack Vector | Risk | Mitigation Required |
|---------------|------|---------------------|
| Slow-read remote: reads data but ACKs slowly | Worker goroutine blocks on write, limiter tokens accumulate unused — burst when connection recovers | See below |
| Network stall: remote drops all packets mid-transfer | Worker goroutine blocks indefinitely — limiter not called | Context deadline / per-operation timeout |
| Slow remote causes limiter backpressure to accumulate tokens above burst cap | Burst of traffic once connection recovers | Enforce burst cap |

### Required Mitigations

1. **Per-operation I/O deadline.** Every SFTP read/write operation MUST have a deadline.
   Use `sftp.Client.SetDeadline` or per-call context cancellation:

```go
// Wrap each chunk I/O operation with a timeout
ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute) // per-chunk max
defer cancel()
// Pass ctx through SFTP operations
```

2. **Token bucket burst cap.** When creating the limiter, set burst to a reasonable
   multiple of the target rate — not unbounded. Recommended: `burst = 2 * (rate per second)`.

```go
// pkg/ratelimit/token_bucket.go
import "golang.org/x/time/rate"

type tokenBucketLimiter struct {
    lim *rate.Limiter
}

func New(bps int64) RateLimiter {
    r := rate.Limit(bps)
    burst := int(bps * 2) // 2-second burst max
    if burst < 1 {
        burst = 1
    }
    return &tokenBucketLimiter{lim: rate.NewLimiter(r, burst)}
}
```

3. **Limiter is called BEFORE the write, not after.** The `Allow(n)` call gates the
   write. If the limiter call itself takes too long (e.g. the bucket is full), it MUST
   respect `context.Done()`:

```go
func (l *tokenBucketLimiter) Allow(ctx context.Context, n int64) error {
    return l.lim.WaitN(ctx, int(n)) // WaitN honours ctx cancellation
}
```

Note: update the `RateLimiter` interface to accept `context.Context` as first argument.

4. **Unlimited mode.** When no bandwidth limit is configured (`BandwidthLimitBps == 0`),
   use a no-op limiter — do not create a `rate.Limiter` with `rate.Inf` and rely on
   internal behaviour. An explicit no-op prevents accidental misuse.

---

## 8. Required golangci-lint / gosec Rules

### Mandatory gosec Rules

Enable these in `.golangci.yml` under `gosec`:

```yaml
linters:
  enable:
    - gosec

linters-settings:
  gosec:
    includes:
      - G101  # Hardcoded credentials
      - G102  # Bind to all interfaces (flag if used in test servers)
      - G103  # Use of unsafe package
      - G104  # Errors unhandled
      - G106  # SSH InsecureIgnoreHostKey
      - G107  # URL provided to HTTP request as taint input
      - G115  # Potential integer overflow when converting between integer types
      - G202  # SQL string formatting (if any DB is added later)
      - G304  # File path provided as taint input
      - G305  # File traversal when extracting zip/tar (if archive support added)
      - G306  # Permissions for new file or directory — flag anything above 0600/0700
      - G401  # Use of weak cryptographic primitive (MD5, SHA1)
      - G402  # TLS InsecureSkipVerify
      - G403  # Use of weak RSA key length
      - G404  # Use of weak random (math/rand vs crypto/rand)
      - G501  # Import blocklist: crypto/md5
      - G502  # Import blocklist: crypto/des
      - G503  # Import blocklist: crypto/rc4
      - G504  # Import blocklist: net/http/cgi
      - G601  # Implicit memory aliasing in for loop (Go < 1.22 concern)
```

### Additional Linters

```yaml
linters:
  enable:
    - errcheck       # All errors must be handled
    - staticcheck    # SA* rules — catches misuse of sync primitives, context, etc.
    - govet          # Detects shadowed variables and printf mismatches
    - noctx          # Detect http.NewRequest without context (if HTTP added)
    - exhaustive     # Enum switch exhaustiveness (for ChunkState, TransferDirection)
    - forbidigo      # Ban specific patterns by regex
```

### forbidigo Patterns (ban in production code)

```yaml
linters-settings:
  forbidigo:
    forbid:
      - p: "ssh\\.InsecureIgnoreHostKey"
        msg: "InsecureIgnoreHostKey is prohibited in production — use knownhosts.New()"
      - p: "ssh\\.Password\\("
        msg: "Password auth is not supported in goscp"
      - p: "ssh\\.PasswordCallback\\("
        msg: "Password auth is not supported in goscp"
      - p: "math/rand"
        msg: "Use crypto/rand for all randomness in goscp"
      - p: "os\\.Chmod.*0[67][0-9][0-9]"
        msg: "Review file permission — ensure not world-readable"
    exclude-godoc-examples: true
    analyze-types: true
```

### Minimum CI Gate

Run in CI before merge:

```bash
golangci-lint run --config .golangci.yml ./...
```

All findings from the enabled rules are hard failures — no warning-only mode for security rules.

---

## Summary: Security Contract Checklist (for T3 implementor)

| # | Requirement | Enforced by |
|---|-------------|-------------|
| 1 | `knownhosts.New()` wired — no InsecureIgnoreHostKey | forbidigo + CI grep |
| 2 | Fail closed on unknown/mismatched host key | Unit test + error classification |
| 3 | Auth: agent → identity file → defaults → fail | Code review + integration test |
| 4 | No password auth | forbidigo |
| 5 | ProxyJump: independent auth per hop, no agent forwarding | Code review |
| 6 | Approved cipher/KEX/MAC set enforced | Config in ClientConfig + integration test |
| 7 | Destination path sanitized and escape-checked | Unit test for `safeDstPath` |
| 8 | No write-through symlinks on destination | Unit test with mock Lstat |
| 9 | Checkpoint loaded as untrusted, schema-validated | Unit test with crafted checkpoint |
| 10 | Checkpoint files written at 0600, directory at 0700 | gosec G306 + unit test |
| 11 | Per-operation I/O timeout on all SFTP calls | Integration test (slow server sim) |
| 12 | Rate limiter uses WaitN(ctx, n) — respects cancellation | Unit test with cancelled context |
| 13 | gosec + forbidigo rules in golangci-lint, run in CI | CI pipeline |
