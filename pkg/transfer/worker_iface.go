package transfer

import "context"

// SFTPClient provides offset-based read and write operations for chunk transfer.
type SFTPClient interface {
	ReadAt(path string, p []byte, off int64) (int, error)
	WriteAt(path string, p []byte, off int64) (int, error)
}

// TransferDirection describes copy direction.
type TransferDirection string

const (
	L2R TransferDirection = "l2r"
	R2L TransferDirection = "r2l"
	R2R TransferDirection = "r2r"
)

// TransferWorker executes one chunk transfer.
type TransferWorker interface {
	Execute(ctx context.Context, chunk Chunk, spec FileTransfer) error
}

// FileTransfer is a concrete transfer operation for one file.
type FileTransfer struct {
	SrcPath    string
	DstPath    string
	SrcHost    string
	DstHost    string
	Direction  TransferDirection
}
