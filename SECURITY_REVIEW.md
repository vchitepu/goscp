# GoSCP Security Review — SSH Implementation & Auth

**Reviewer:** Legolas (Security)
**Date:** 2026-05-25
**Scope:** pkg/ssh, pkg/sftp, pkg/checkpoint, pkg/path, pkg/transfer, cmd/goscp/run.go
**Dependency audit:** go mod verify + golang.org/x/crypto v0.52.0 CVE check

---

## Findings Table

| # | Severity | Area | Description | Location | Recommendation |
|---|----------|------|-------------|----------|----------------|
| F1 | **Medium** | Checkpoint tampering | `--resume <id>` is passed directly into `filepath.Join` with no validation. A crafted id like `../../etc/cron.d/evil` causes path traversal within the checkpoint dir. | `pkg/checkpoint/checkpointer.go: Load(), Delete(), pathForID()` | **Fixed in this review** — `sanitizeID()` now validates the id is exactly 16 hex chars before any filesystem op. |
| F2 | **Low** | Private key handling | Raw PEM bytes from `os.ReadFile` linger in Go heap until GC. No explicit zero after `ssh.ParsePrivateKey`. | `pkg/ssh/dialer.go: loadPrivateKeySigner()` | **Fixed in this review** — `defer` loop zeros `keyBytes` immediately after parse. Note: Go's GC may have copied the slice; this is best-effort mitigation. True elimination requires `x/sys/unix.Mlock` + locked memory, noted as a follow-up. |
| F3 | **Low** | SSH agent socket | Agent connection is opened, signers extracted, then the unix socket connection is **closed** before returning the auth method. The returned `ssh.PublicKeys(signers...)` holds cached `ssh.Signer` objects, not a live agent connection — so agent-based signing works but revocation (key deletion from agent) is not respected mid-session. | `pkg/ssh/dialer.go: buildSSHAgentAuth()` | For short-lived transfers this is acceptable. For long-running transfers, consider holding the agent connection open and using `agent.NewClient(conn)` directly as an `ssh.AuthMethod` (via `ssh.PublicKeysCallback`). |
| F4 | **Low** | ProxyJump credential reuse | All hops in a ProxyJump chain share a **single** `ssh.ClientConfig` (same user, same auth methods, same known_hosts). If intermediate bastions require different credentials or separate known_hosts entries, the current design silently fails or uses wrong creds. | `pkg/ssh/dialer.go: Dial()` | Document the limitation. If per-hop credentials are needed, `DialOptions` must be extended with per-hop configs. |
| F5 | **Info** | Path traversal (remote write) | The SFTP `Create()` path is fully server-controlled. A malicious SFTP server could respond with a crafted path causing writes outside intended destinations. | `pkg/sftp/client.go: Create()` | This is inherent to the SFTP protocol — mitigation requires validating the resolved destination path against the intended base before opening. Track as hardening item. |
| F6 | **Info** | Symlink attacks (ReadDir) | `pkg/sftp: ReadDir` delegates to `github.com/pkg/sftp` which follows server-side symlinks. A malicious server can use symlinks to expose or overwrite unintended files during R2R transfers. | `pkg/sftp/client.go: ReadDir()` | Hardening: stat each entry after ReadDir and reject symlinks pointing outside the declared transfer root. |
| F7 | **Info** | Rate limiter bypass | `waitForBytes` uses `limiter.WaitN` with `int(step)` cast from uint64. If burst < 1 it is clamped to 1, preventing panic. However, `LimitMbps` is caller-supplied with no upper bound — a very large value creates a limiter with an enormous burst and is effectively unlimited. | `pkg/transfer/scheduler.go: waitForBytes()` | Add a max cap on `LimitMbps` at the CLI flag level (e.g. 100 Gbps = 102400 Mbps). |
| F8 | **Info** | Write atomicity | `localFileSFTP.WriteAt` opens the destination file with `O_WRONLY|O_CREATE` but not `O_SYNC`. On crash mid-transfer, partial chunk writes leave the file in a corrupted state. The checkpoint records which chunks completed so resume is safe, but the partially-written destination file is not cleaned up. | `cmd/goscp/run.go: WriteAt()` | On resume, truncate or re-verify the destination file before writing resumed chunks. |
| F9 | **Info** | Dependency: golang.org/x/crypto v0.52.0 | `go mod verify` passes — no tampered modules. x/crypto v0.52.0 has no known critical CVEs as of this review. The closest advisory is GO-2023-1840 (fixed in v0.17.0); v0.52.0 is well past that. | go.mod | No action required. Monitor via `govulncheck` in CI. |

---

## Items Confirmed Secure

1. **SSH host key verification** — `knownhosts.New()` is mandatory; `KnownHostsPath == ""` returns an error before any connection attempt. No `InsecureIgnoreHostKey` anywhere in production code. Test fixtures use real temporary known_hosts files (not InsecureIgnoreHostKey). CONFIRMED SECURE.

2. **buildAuthMethods guards** — requires at least one auth method; fails closed if neither key nor agent is available. CONFIRMED SECURE.

3. **Checkpoint file permissions** — directory created at `0700`, file written at `0600`. No world-readable checkpoint exposure. CONFIRMED SECURE.

4. **Checkpoint ID generation** — SHA-256 over source paths + destination + timestamp, truncated to 16 hex chars. Collision risk negligible for practical use. CONFIRMED SECURE.

5. **SSH agent: no ForwardAgent** — no `RequestAgentForwarding` call anywhere. Agent socket is not forwarded to remote hosts. CONFIRMED SECURE.

6. **Context propagation** — all dial and worker paths respect `ctx.Done()`. Cancellation is prompt on signal. CONFIRMED SECURE.

7. **Partial write detection** — `worker.go` checks `nWrite != nRead` and returns an error. Short writes are surfaced as failures, not silent corruption. CONFIRMED SECURE.

8. **go mod verify** — all modules verified against their checksums in go.sum. No tampered dependencies. CONFIRMED SECURE.

---

## Fixes Applied in This Review

### F1 — Checkpoint ID path traversal (pkg/checkpoint/checkpointer.go)

Added `sanitizeID()` using a strict allowlist regex (`^[0-9a-f]{16}$`).
Called in `Load()` and `Delete()` before any filesystem operation.
The regex matches exactly the output of `ComputeCheckpointID` and rejects any string that could navigate outside `~/.goscp/checkpoints/`.

### F2 — Private key bytes zeroing (pkg/ssh/dialer.go)

Added a `defer` that zeroes `keyBytes` immediately after `ssh.ParsePrivateKey` returns.
This reduces the window during which the raw PEM is in memory, though Go's GC may have already made copies of the backing array. Full elimination requires `mlock`-pinned memory (noted as follow-up, not done here to avoid a syscall dependency).

---

## Follow-up Tasks (not fixed here — separate issues)

- Install `govulncheck` in CI and gate on zero findings.
- Add `golangci-lint` forbidigo rule banning `InsecureIgnoreHostKey` in non-test files.
- Consider `mlock` for private key material if this tool is used in high-security environments.
- Add an upper bound on `--limit-mbps` flag (e.g. 102400).
- On resume, verify/truncate partial destination files before writing resumed chunks.
