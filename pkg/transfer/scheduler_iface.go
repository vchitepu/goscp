package transfer

import (
	"context"

	gssh "golang.org/x/crypto/ssh"
)

// SSHDialerPool acquires and releases SSH connection pairs for scheduler workers.
type SSHDialerPool interface {
	Acquire(ctx context.Context, direction TransferDirection) (srcConn *gssh.Client, dstConn *gssh.Client, err error)
	Release(srcConn *gssh.Client, dstConn *gssh.Client)
}

// WorkerFactory creates transfer workers for scheduler goroutines.
type WorkerFactory interface {
	New(srcConn *gssh.Client, dstConn *gssh.Client, direction TransferDirection) (TransferWorker, error)
}

// TransferScheduler schedules chunks to workers and streams results.
type TransferScheduler interface {
	Schedule(ctx context.Context, chunks []Chunk, spec FileTransfer, factory WorkerFactory) <-chan Result
}

// Result is one chunk execution outcome.
type Result struct {
	ChunkID          string
	BytesTransferred uint64
	Err              error
}

// SchedulerConfig controls scheduler worker count and optional bandwidth limit.
type SchedulerConfig struct {
	NumWorkers int
	LimitMbps  int
}
