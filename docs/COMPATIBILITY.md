# GoSCP ↔ mscp Compatibility Contract

Status: Draft contract for implementation and test assertions (feeds T8 and T10).

Scope: This document defines what GoSCP treats as compatibility with `mscp` (https://github.com/upa/mscp), what is intentionally different, and why.

## 1) Flag surface parity

Compatibility principle:
- GoSCP keeps mscp-style short flags where practical.
- GoSCP also provides clear long flags for script readability.
- Where mscp behavior is ambiguous or too broad, GoSCP narrows behavior intentionally and documents it.

Important note on `-c`:
- In upstream mscp, `-c` is cipher selection and checkpoint write is `-W`.
- In GoSCP contract for this project, `-c` is reserved for checkpoint path (per project requirement), and cipher selection is intentionally omitted from the parity set.

| mscp flag | mscp meaning | GoSCP flag | Contract |
|---|---|---|---|
| `-n` | number of SSH connections | `-n`, `--connections` | Parity: same intent. |
| `-s` | minimum chunk size | `-s`, `--sftp-sessions` | Intentional divergence: in GoSCP, `-s` means SFTP sessions (project-defined UX), not min chunk size. Min chunk size exposed as long-only `--min-chunk-size` if needed later. |
| `-S` | maximum chunk size | `-S`, `--chunk-size` | Partial parity: GoSCP treats chunk size as target/fixed chunk size (not mscp max-only). |
| `-L` | bitrate limit | `-L`, `--limit` | Parity: transfer rate limit. |
| `-i` | identity file | `-i`, `--identity` | Parity. |
| `-F` | SSH config file | `-F`, `--ssh-config` | Parity. |
| `-l` | login user | `-l`, `--login-name` | Parity. |
| `-P` | SSH port | `-P`, `--port` | Parity. |
| `-o` | ssh_config option override | `-o`, `--ssh-option` (repeatable) | Parity. |
| `-4` | force IPv4 | `-4` | Parity. |
| `-6` | force IPv6 | `-6` | Parity. |
| `-C` | SSH compression mode | `-C`, `--compress` | Parity at intent level (enable/configure compression). |
| `-c` | (mscp: cipher) | `-c`, `--checkpoint` | Intentional divergence: GoSCP uses `-c` for checkpoint file path; mscp uses `-W`. |
| `-R` | resume from checkpoint | `-R`, `--resume` | Parity at user intent. |
| `-D` | dry-run | `-D`, `--dry-run` | Parity. |
| `-q` | quiet | `-q`, `--quiet` | Parity. |
| `-v` | verbose (repeatable) | `-v`, `--verbose` (repeatable) | Parity. |

Flags intentionally omitted from this contract (with rationale):
- mscp `-W` (checkpoint write): omitted because GoSCP standardizes checkpoint write on `-c`.
- mscp `-m`, `-u`, `-I`, `-a`, `-b`, `-M`, `-g`, `-N`, `-p`, `-r`, `-d`, `-J`: outside current parity scope for T8/T10 or replaced by cleaner Go-native defaults/long flags.

## 2) Path semantics

### 2.1 Remote spec parsing: `[user@]host:path`

mscp behavior (from source):
- First non-escaped `@` splits user/host.
- First non-escaped `:` outside IPv6 brackets splits host/path.
- IPv6 bracket forms like `[2001:db8::1]:/path` are accepted.
- Escaped separators (`\@`, `\:`) are treated as literals.
- For remote path shorthand:
  - empty path after colon (`host:`) resolves to `.`
  - `host:~` resolves to `.`
  - `host:~/...` resolves to path relative to remote home.

GoSCP contract:
- Match mscp/scp parsing rules above.
- Preserve bracketed IPv6 handling and escaped separator semantics.
- Preserve remote shorthand normalization (`:`, `:~`, `:~/...`).

### 2.2 Local glob expansion

mscp behavior:
- Uses `glob(..., GLOB_NOCHECK, ...)` for source patterns.
- If no match, pattern is retained literally (NOCHECK behavior).
- Supports remote glob via alternate dir functions on platforms with `GLOB_ALTDIRFUNC`; on musl remote globbing degrades and may behave as literal.

GoSCP contract:
- For local sources, perform deterministic glob expansion with NOCHECK-equivalent semantics:
  - match exists -> expand
  - no match -> keep literal token
- Remote glob behavior is not guaranteed as a compatibility objective; quoted remote globs may be treated literally depending on remote adapter. This is an intentional narrowing for predictability across libc/platforms.

### 2.3 Trailing slash semantics

mscp behavior:
- Destination ending in `/` forces destination-as-directory behavior.
- Multiple source arguments force destination-as-directory behavior.
- If source is a directory and destination exists as directory: copy the source directory tree under destination (include source basename).
- If source is a directory and destination does not exist: destination path becomes the renamed root (copy directory contents into newly created destination root).

GoSCP contract:
- Match these semantics exactly for local↔remote directions.
- Integration assertions (T10) should include:
  - single-file to existing dir
  - single-file to non-existent path
  - multi-source to dir
  - dir→existing dir (retain source basename)
  - dir→non-existent dst (rename root)

### 2.4 Divergence note: remote-to-remote

mscp does not support remote-to-remote copy.
GoSCP intentionally diverges by supporting R2R via relay architecture.

## 3) Exit codes

GoSCP contract (strict and testable):
- `0` = full success (all planned files/chunks completed)
- `1` = partial failure (at least one file/chunk failed, but run completed)
- `2` = fatal failure (setup/runtime fatal: auth failure, host unreachable, invalid config/input, unrecoverable checkpoint error)

mscp reference point:
- mscp documents `0` success and `>0` error (single broad error class).
- GoSCP intentionally improves this to explicit tri-state codes for CI and automation.

## 4) Checkpoint file format

### 4.1 mscp reference

mscp checkpoint format is a custom binary object stream:
- file header (`magic`, `version`)
- typed objects (`meta`, `path`, `chunk`)
- network byte order encoding

### 4.2 GoSCP format (JSON)

GoSCP uses JSON checkpoints, based on GoSCP model types defined in architecture (`CheckpointState`, `FileTransfer`, `Chunk`, etc.).

Canonical JSON schema (contract level):

```json
{
  "id": "string",
  "transfer_id": "string",
  "created_at": "RFC3339 timestamp",
  "updated_at": "RFC3339 timestamp",
  "files": [
    {
      "id": "string",
      "direction": "l2r|r2l|r2r",
      "source": {
        "host": "string or empty",
        "user": "string",
        "port": 22,
        "path": "string",
        "is_remote": true
      },
      "dest": {
        "host": "string or empty",
        "user": "string",
        "port": 22,
        "path": "string",
        "is_remote": false
      },
      "file": {
        "id": "string",
        "path": "string",
        "size": 0,
        "mode": 420,
        "mod_time": "RFC3339 timestamp",
        "is_dir": false,
        "remote_host": "string"
      },
      "chunks": [
        {
          "id": "string",
          "file_id": "string",
          "offset": 0,
          "length": 0,
          "done": 0,
          "state": "pending|in_progress|done|failed",
          "attempt": 0,
          "err_msg": "string"
        }
      ]
    }
  ],
  "chunk_index": {
    "chunk-id": {
      "id": "chunk-id",
      "file_id": "string",
      "offset": 0,
      "length": 0,
      "done": 0,
      "state": "pending|in_progress|done|failed",
      "attempt": 0,
      "err_msg": "string"
    }
  },
  "failed_chunks": [
    {
      "id": "string",
      "file_id": "string",
      "offset": 0,
      "length": 0,
      "done": 0,
      "state": "failed",
      "attempt": 1,
      "err_msg": "string"
    }
  ],
  "meta": {
    "k": "v"
  }
}
```

Compatibility statement (must be explicit):
- GoSCP checkpoint files are NOT compatible with mscp checkpoint files.
- No read/write interop is guaranteed in either direction.

## 5) Intentional improvements over mscp

1) ProxyJump support
- GoSCP treats jump-host routing as a first-class behavior (including multi-hop strategy), while mscp support is tied to libssh/OpenSSH proxy handling constraints.

2) Resume granularity
- mscp tracks unfinished chunks.
- GoSCP contract targets per-byte progress accounting inside chunks (`done` field), enabling finer resume precision and reduced retransmit after interruption.

3) No CGO / no libnuma dependency
- mscp depends on libssh and native build toolchain details.
- GoSCP is pure Go runtime/build (no CGO requirement in default path), reducing operational friction.

4) `go install` delivery model
- GoSCP is installable as a standard Go module/binary, aligned with Go tooling workflows.

5) Embeddable library API (`pkg/`)
- GoSCP is designed as both CLI and reusable library surface.
- mscp is primarily a CLI with C library internals, not the same Go-native embedding model.

## 6) Known intentional divergences (with rationale)

1) `-c` semantics diverge from mscp
- Divergence: mscp `-c` = cipher; GoSCP `-c` = checkpoint path.
- Rationale: checkpoint/resume is core UX in GoSCP and needs a short ergonomic flag in this project contract.

2) Exit code taxonomy diverges
- Divergence: mscp uses broad `>0` error; GoSCP uses `1` partial and `2` fatal.
- Rationale: enables robust automation and deterministic CI assertions.

3) Remote-to-remote behavior diverges
- Divergence: mscp does not support R2R; GoSCP supports R2R relay.
- Rationale: required capability for lab and distributed transfer workflows.

4) Chunk-size semantics under `-S` are normalized
- Divergence: mscp `-S` is max chunk size with derived defaults; GoSCP `-S` is contractually treated as chunk-size control for simpler operator mental model.
- Rationale: clearer behavior and easier reproducibility in tests.

5) Remote glob compatibility is narrowed
- Divergence: mscp behavior depends partly on libc/platform support (`GLOB_ALTDIRFUNC`); GoSCP does not promise full remote glob parity.
- Rationale: portability and deterministic cross-platform behavior.

6) Advanced transport tuning flags out of current parity set
- Divergence: several mscp transport micro-tuning flags are omitted from immediate parity scope.
- Rationale: prioritize stable core compatibility first; introduce advanced knobs later behind explicit long flags.

---

Testability notes for T10:
- Assert exact tri-state exit codes (0/1/2).
- Assert path semantics matrix for dir/file + trailing slash combinations.
- Assert checkpoint file is JSON and explicitly rejected if mscp binary checkpoint is provided.
- Assert flag parsing/mapping table in this document as contract source of truth.
