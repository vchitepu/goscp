package transfer

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// DefaultTransferWorker is the production TransferWorker implementation.
type DefaultTransferWorker struct {
	sftp SFTPClient
	log  *logrus.Entry
}

// NewDefaultTransferWorker creates a transfer worker.
func NewDefaultTransferWorker(sftp SFTPClient, log *logrus.Entry) *DefaultTransferWorker {
	if log == nil {
		log = logrus.WithField("component", "transfer_worker")
	}
	return &DefaultTransferWorker{sftp: sftp, log: log}
}

func (w *DefaultTransferWorker) Execute(ctx context.Context, chunk Chunk, spec FileTransfer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chunk.Length == 0 {
		return nil
	}

	buf := make([]byte, chunk.Length)
	nRead, readErr := w.sftp.ReadAt(spec.SrcPath, buf, int64(chunk.Offset))
	if readErr != nil && readErr != io.EOF {
		return errors.Wrapf(readErr, "read chunk at offset %d", chunk.Offset)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if nRead <= 0 {
		return errors.Errorf("read chunk at offset %d: no bytes read", chunk.Offset)
	}

	nWrite, writeErr := w.sftp.WriteAt(spec.DstPath, buf[:nRead], int64(chunk.Offset))
	if writeErr != nil {
		return errors.Wrapf(writeErr, "write chunk at offset %d", chunk.Offset)
	}
	if nWrite != nRead {
		return errors.Errorf("partial write at offset %d: wrote %d of %d", chunk.Offset, nWrite, nRead)
	}

	atomic.AddUint64(&chunk.Done, uint64(nWrite))
	return nil
}
