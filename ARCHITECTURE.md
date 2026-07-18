# GoSCP Architecture

This document defines the architecture and interfaces for GoSCP: a fully Go-native reimplementation of mscp with multithreaded SCP semantics, chunked transfer, checkpoint/resume, and remote-to-remote (R2R) copy.

## 1) Module Layout

Repository layout:

```text
goscp/
├── cmd/
│   └── goscp/
│       ├── main.go
│       ├── root.go
│       ├── copy.go
│       └── config.go
├── pkg/
│   ├── model/
│   │   ├── file_info.go
│   │   ├── chunk.go
│   │   ├── transfer_spec.go
│   │   └── checkpoint_state.go
│   ├── ssh/
│   │   ├── dialer_iface.go
│   │   ├── dialer.go
│   │   └── known_hosts.go
│   ├── sftp/
│   │   ├── client_iface.go
│   │   └── client.go
│   ├── chunking/
│   │   ├── file_chunker_iface.go
│   │   └── fixed_chunker.go
│   ├── scheduling/
│   │   ├── transfer_scheduler_iface.go
│   │   ├── transfer_worker_iface.go
│   │   └── channel_scheduler.go
│   ├── checkpoint/
│   │   ├── checkpointer_iface.go
│   │   └── file_checkpointer.go
│   ├── resolve/
│   │   ├── path_resolver_iface.go
│   │   └── default_resolver.go
│   ├── progress/
│   │   ├── progress_reporter_iface.go
│   │   ├── mpb_reporter.go
│   │   └── noop_reporter.go
│   ├── ratelimit/
│   │   ├── rate_limiter_iface.go
│   │   └── token_bucket.go
│   └── engine/
│       ├── planner.go
│       ├── executor.go
│       └── coordinator.go
├── internal/
│   ├── errs/
│   │   ├── codes.go
│   │   └── classify.go
│   ├── logging/
│   │   └── logger.go
│   ├── retry/
│   │   └── retry.go
│   └── iox/
│       └── copybuf.go
├── test/
│   └── integration/
│       ├── l2r_test.go
│       ├── r2l_test.go
│       ├── r2r_relay_test.go
│       └── checkpoint_resume_test.go
└── go.mod
```

Responsibilities:
- `pkg/`: public, importable API surface (library use + CLI internals).
- `cmd/goscp/`: CLI entrypoint, command wiring, config parsing, and invocation.
- `internal/`: implementation details not intended for public imports.
- `test/integration/`: end-to-end integration tests against real/local SSH/SFTP fixtures.

## Contract alignment note

To normalize package naming and reduce long-term contract drift, architecture paths are canonicalized as:
- `pkg/sshx` -> `pkg/ssh`
- `pkg/sftpx` -> `pkg/sftp`

This is a documentation normalization only and does not change runtime behavior by itself.

## 2) Core Interfaces

Guiding rule: noun-verber naming, one interface per `*_iface.go` file, minimal methods for testability.

### `SSHDialer` (`pkg/ssh/dialer_iface.go`)

```go
package ssh

import (
    "context"

    "golang.org/x/crypto/ssh"
)

type SSHDialer interface {
    Dial(ctx context.Context, host string, cfg *ssh.ClientConfig) (*ssh.Client, error)
    Close() error
}
```

### `SFTPClient` (`pkg/sftp/client_iface.go`)

```go
package sftp

import "os"

type SFTPClient interface {
    Open(path string) (io.ReadCloser, error)
    Create(path string) (io.WriteCloser, error)
    Stat(path string) (os.FileInfo, error)
    ReadDir(path string) ([]os.FileInfo, error)
    MkdirAll(path string, perm os.FileMode) error
    Close() error
}
```

### `FileChunker` (`pkg/chunking/file_chunker_iface.go`)

```go
package chunking

import "github.com/vchitepu/goscp/pkg/model"

type FileChunker interface {
    Chunk(file model.FileInfo, cfg model.ChunkConfig) ([]model.Chunk, error)
}
```

### `TransferScheduler` (`pkg/scheduling/transfer_scheduler_iface.go`)

```go
package scheduling

import (
    "context"

    "github.com/vchitepu/goscp/pkg/model"
)

type TransferScheduler interface {
    Schedule(ctx context.Context, chunks []model.Chunk, factory WorkerFactory) <-chan model.Result
}
```

### `TransferWorker` (`pkg/scheduling/transfer_worker_iface.go`)

```go
package scheduling

import (
    "context"

    "github.com/vchitepu/goscp/pkg/model"
)

type TransferWorker interface {
    Execute(ctx context.Context, chunk model.Chunk, spec model.FileTransfer) error
}
```

### `Checkpointer` (`pkg/checkpoint/checkpointer_iface.go`)

```go
package checkpoint

import "github.com/vchitepu/goscp/pkg/model"

type Checkpointer interface {
    Save(state model.CheckpointState) error
    Load(id string) (model.CheckpointState, error)
    UpdateChunk(id string, chunk model.Chunk) error
    Delete(id string) error
}
```

### `PathResolver` (`pkg/resolve/path_resolver_iface.go`)

```go
package resolve

import "github.com/vchitepu/goscp/pkg/model"

type PathResolver interface {
    Resolve(srcs []model.PathSpec, dst model.PathSpec) ([]model.FileTransfer, error)
}
```

### `ProgressReporter` (`pkg/progress/progress_reporter_iface.go`)

```go
package progress

import "github.com/vchitepu/goscp/pkg/model"

type ProgressReporter interface {
    Add(f model.FileInfo)
    Update(r model.Result)
    Complete()
    Fail(f model.FileInfo, err error)
}
```

### `RateLimiter` (`pkg/ratelimit/rate_limiter_iface.go`)

```go
package ratelimit

type RateLimiter interface {
    Allow(n int64) error
}
```

Supporting type alias in scheduler package:

```go
type WorkerFactory func() TransferWorker
```

## 3) Data Types

Canonical model types in `pkg/model`:

```go
package model

import (
    "time"
)

type ChunkState string

const (
    ChunkPending    ChunkState = "pending"
    ChunkInProgress ChunkState = "in_progress"
    ChunkDone       ChunkState = "done"
    ChunkFailed     ChunkState = "failed"
)

type TransferDirection string

const (
    L2R TransferDirection = "l2r"
    R2L TransferDirection = "r2l"
    R2R TransferDirection = "r2r"
)

type FileInfo struct {
    ID         string
    Path       string
    Size       int64
    Mode       uint32
    ModTime    time.Time
    IsDir      bool
    RemoteHost string
}

type Chunk struct {
    ID      string
    FileID  string
    Offset  int64
    Length  int64
    Done    int64
    State   ChunkState
    Attempt int
    ErrMsg  string
}

type PathSpec struct {
    Host     string // empty for local
    User     string
    Port     int
    Path     string
    IsRemote bool
}

type FileTransfer struct {
    ID        string
    Direction TransferDirection
    Source    PathSpec
    Dest      PathSpec
    File      FileInfo
    Chunks    []Chunk
}

type ChunkConfig struct {
    ChunkSize      int64
    MinChunkSize   int64
    MaxChunkSize   int64
    Adaptive       bool
}

type TransferConfig struct {
    Workers           int
    BufferSize        int
    RetryMax          int
    RetryBackoff      time.Duration
    PreserveTimes     bool
    PreserveMode      bool
    VerifyChecksum    bool
    CheckpointDir     string
    BandwidthLimitBps int64
}

type TransferSpec struct {
    ID      string
    Sources []PathSpec
    Dest    PathSpec
    Config  TransferConfig
}

type CheckpointState struct {
    ID           string
    TransferID   string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    Files        []FileTransfer
    ChunkIndex   map[string]Chunk
    FailedChunks []Chunk
    Meta         map[string]string
}

type Result struct {
    TransferID string
    FileID     string
    ChunkID    string
    Bytes      int64
    State      ChunkState
    Err        error
    StartedAt  time.Time
    EndedAt    time.Time
}
```

## 4) Concurrency Model

Design:
- Use a buffered `workCh chan ChunkWorkItem` as queue.
- Spawn `N` worker goroutines (`N = TransferConfig.Workers`).
- Scheduler enqueues chunk work; workers pull, execute, and emit `Result` into buffered `resultCh`.
- Cancellation propagates only through `context.Context` (scheduler, workers, planner, CLI signal handler).
- Prefer channels over shared mutable state; avoid mutexes where channel ownership suffices.

Execution flow:
1. Planner resolves `[]FileTransfer` and chunks each file.
2. Scheduler writes all pending chunks to `workCh`.
3. Workers process chunks and emit `Result`.
4. Coordinator consumes `Result`, updates checkpoint/progress, and decides retries.
5. On `ctx.Done()`, all producers/consumers stop gracefully and flush checkpoint.

Retry handling:
- Failed chunk is re-enqueued with incremented `Attempt` until `RetryMax`.
- Exceeded retries marks chunk `ChunkFailed` and transfer partially failed.

## 5) SSH/SFTP Strategy

Libraries:
- SSH: `golang.org/x/crypto/ssh`
- SFTP: `github.com/pkg/sftp`

Security requirements:
- `known_hosts` verification is mandatory (no insecure ignore-host-key mode).
- Host key callback built via `knownhosts.New(...)` and wired into `ssh.ClientConfig.HostKeyCallback`.

ProxyJump:
- Implement chained dialing in `SSHDialer`:
  - Dial jump host A.
  - From A, dial host B (`client.Dial("tcp", target)`), wrap with `ssh.NewClientConn`.
  - Continue chain for multi-hop paths.
- Return final `*ssh.Client` for SFTP session creation.

SFTP lifecycle:
- Per worker: acquire client/session, process chunk I/O, close handles promptly.
- Reuse SSH client where practical, but avoid unbounded shared client state.

## 6) Dependencies

Required dependencies and intended roles:
- `github.com/sirupsen/logrus`: structured logging with fields (`transfer_id`, `file_id`, `chunk_id`, `host`).
- `github.com/spf13/afero`: all local filesystem operations for testability and mock support.
- `github.com/spf13/cobra` + `github.com/spf13/viper`: CLI commands, flags, env/config loading.
- `github.com/stretchr/testify`: assertions, mocks, test suites.
- `github.com/vbauerster/mpb/v8`: multi-progress bars for interactive CLI output.
- `golang.org/x/time/rate`: token-bucket limiter backing `RateLimiter`.

## 7) Remote-to-Remote (R2R) Strategy

Current strategy: local relay (authoritative baseline)
- Read from remote source over SFTP into buffered stream.
- Write to remote destination over SFTP.
- Chunking/checkpointing works exactly as other directions; only endpoint adapters differ.

Rationale:
- Simpler correctness model.
- Consistent retry and checkpoint semantics.
- No remote shell coupling or ad-hoc command execution.

Future strategy (deferred): direct remote tunnel
- Establish SSH tunnel between remotes (or remote-exec scp/sftp stream).
- Keep same planner/checkpoint model; only transfer worker implementation changes.

## 8) Error Handling and Observability

Principles:
- Explicit error returns everywhere; no panic-driven control flow.
- Wrap errors with operation and identity context (`fmt.Errorf("copy chunk %s: %w", chunkID, err)`).
- Classify recoverable vs terminal failures in `internal/errs`.
- Log with logrus structured fields; avoid free-form-only logs.
- Track partial success by file/chunk status to support resume and post-run reporting.

Failure classes:
- Retryable: transient SSH disconnect, temporary network timeout, short write/read, channel reset.
- Non-retryable: permission denied, not found (source), path conflict, host key mismatch.

Checkpoint behavior on failure:
- Persist chunk state transitions (`pending -> in_progress -> done/failed`).
- Ensure best-effort save on cancellation and SIGINT.

## 9) Package Dependency Graph (No Circular Imports)

Rules:
- `cmd/goscp` imports from `pkg/*` only.
- `pkg/*` may import `internal/*` for implementation helpers.
- `internal/*` must not import `cmd/*`.
- `pkg/model` is foundational and imported by other `pkg` packages.

Graph:

```text
cmd/goscp
  -> pkg/engine
  -> pkg/resolve
  -> pkg/progress
  -> pkg/model

pkg/engine
  -> pkg/model
  -> pkg/chunking
  -> pkg/scheduling
  -> pkg/checkpoint
  -> pkg/ratelimit
  -> pkg/ssh
  -> pkg/sftp
  -> internal/errs
  -> internal/logging

pkg/{chunking,scheduling,checkpoint,resolve,progress,ratelimit,ssh,sftp}
  -> pkg/model
  -> internal/* (as needed)

test/integration
  -> cmd/goscp (CLI e2e) and/or pkg/engine APIs
```

No package in `pkg/*` imports another package that creates an import cycle; orchestrating dependencies are pushed upward into `pkg/engine`.

## CLI-to-Engine Contract

`cmd/goscp` responsibilities:
- Parse source/destination URI/path specs.
- Build `TransferSpec` + `TransferConfig` from flags/config.
- Instantiate concrete implementations for required interfaces.
- Wire cancellation via context + signal handling.
- Invoke engine coordinator and return proper exit codes.

Exit codes (recommended):
- `0`: all transfers complete.
- `1`: user/config/runtime fatal error.
- `2`: partial success (some chunks/files failed).

## Implementation Sequencing (Recommended)

1. `pkg/model` (types/constants).
2. Interface files (`*_iface.go`) in each package.
3. Local/remote path resolution (`pkg/resolve`).
4. Chunker + scheduler + worker skeleton.
5. SSH dialer with known_hosts + SFTP adapter.
6. Checkpointer implementation.
7. Engine coordinator.
8. CLI wiring (`cmd/goscp`).
9. Integration tests (`test/integration`).

This ordering keeps architecture enforceable while enabling incremental execution and verification.
