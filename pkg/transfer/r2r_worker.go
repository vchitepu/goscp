package transfer

import (
	"context"
	"io"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	sftppkg "github.com/vchitepu/goscp/pkg/sftp"
)

const maxR2RBufferSize uint64 = 4 * 1024 * 1024 // 4MB

// R2RTransferWorker relays chunk data from a source remote host to a destination remote host.
type R2RTransferWorker struct {
	src sftppkg.SFTPClient
	dst sftppkg.SFTPClient
	log *logrus.Entry
}

// NewR2RTransferWorker creates a remote-to-remote transfer worker.
func NewR2RTransferWorker(src sftppkg.SFTPClient, dst sftppkg.SFTPClient, log *logrus.Entry) *R2RTransferWorker {
	if log == nil {
		log = logrus.WithField("component", "r2r_transfer_worker")
	}
	return &R2RTransferWorker{src: src, dst: dst, log: log}
}

// Execute relays one chunk using streaming copy with bounded buffer size.
func (w *R2RTransferWorker) Execute(ctx context.Context, chunk Chunk, spec FileTransfer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chunk.Length == 0 {
		return nil
	}

	entry := w.log.WithFields(logrus.Fields{
		"src_host":     spec.SrcHost,
		"dst_host":     spec.DstHost,
		"chunk_offset": chunk.Offset,
		"chunk_length": chunk.Length,
		"src_path":     spec.SrcPath,
		"dst_path":     spec.DstPath,
	})
	entry.Debug("r2r chunk transfer start")

	srcReader, err := w.src.Open(spec.SrcPath)
	if err != nil {
		return errors.Wrapf(err, "open source %s", spec.SrcPath)
	}
	defer func() {
		_ = srcReader.Close()
	}()

	dstWriter, err := w.dst.Create(spec.DstPath)
	if err != nil {
		return errors.Wrapf(err, "create destination %s", spec.DstPath)
	}
	defer func() {
		_ = dstWriter.Close()
	}()

	if err := seekSourceToChunk(srcReader, chunk.Offset); err != nil {
		return errors.Wrapf(err, "seek source to offset %d", chunk.Offset)
	}
	if err := seekDestinationToChunk(dstWriter, chunk.Offset); err != nil {
		return errors.Wrapf(err, "seek destination to offset %d", chunk.Offset)
	}

	bufSize := chunk.Length
	if bufSize > maxR2RBufferSize {
		bufSize = maxR2RBufferSize
	}
	buf := make([]byte, int(bufSize))

	remaining := chunk.Length
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}

		toRead := len(buf)
		if uint64(toRead) > remaining {
			toRead = int(remaining)
		}

		nr, readErr := srcReader.Read(buf[:toRead])
		if readErr != nil && readErr != io.EOF {
			return errors.Wrapf(readErr, "read source at offset %d", chunk.Offset+(chunk.Length-remaining))
		}
		if nr == 0 {
			if readErr == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return errors.Errorf("read source at offset %d: no bytes read", chunk.Offset+(chunk.Length-remaining))
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		nw, writeErr := dstWriter.Write(buf[:nr])
		if writeErr != nil {
			return errors.Wrapf(writeErr, "write destination at offset %d", chunk.Offset+(chunk.Length-remaining))
		}
		if nw != nr {
			return io.ErrShortWrite
		}

		remaining -= uint64(nr)
	}

	entry.Debug("r2r chunk transfer complete")
	return nil
}

func seekSourceToChunk(r io.Reader, off uint64) error {
	if off == 0 {
		return nil
	}
	if seeker, ok := r.(io.Seeker); ok {
		_, err := seeker.Seek(int64(off), io.SeekStart)
		return err
	}

	_, err := io.CopyN(io.Discard, r, int64(off))
	return err
}

func seekDestinationToChunk(w io.Writer, off uint64) error {
	if off == 0 {
		return nil
	}
	seeker, ok := w.(io.Seeker)
	if !ok {
		return errors.New("destination writer does not support seek")
	}
	_, err := seeker.Seek(int64(off), io.SeekStart)
	return err
}
