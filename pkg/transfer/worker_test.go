package transfer

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workerFakeSFTP struct {
	readFn  func(path string, p []byte, off int64) (int, error)
	writeFn func(path string, p []byte, off int64) (int, error)
}

func (f *workerFakeSFTP) ReadAt(path string, p []byte, off int64) (int, error) {
	if f.readFn == nil {
		return 0, nil
	}
	return f.readFn(path, p, off)
}

func (f *workerFakeSFTP) WriteAt(path string, p []byte, off int64) (int, error) {
	if f.writeFn == nil {
		return len(p), nil
	}
	return f.writeFn(path, p, off)
}

func TestExecute_Success(t *testing.T) {
	t.Parallel()

	src := []byte("abcdefgh")
	var readOffset, writeOffset int64
	var wrote []byte

	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			assert.Equal(t, "/src.bin", path)
			readOffset = off
			copy(p, src)
			return len(src), nil
		},
		writeFn: func(path string, p []byte, off int64) (int, error) {
			assert.Equal(t, "/dst.bin", path)
			writeOffset = off
			wrote = append(wrote[:0], p...)
			return len(p), nil
		},
	}, logrus.NewEntry(logrus.New()))

	chunk := Chunk{ID: "c1", Offset: 16, Length: uint64(len(src))}
	err := w.Execute(context.Background(), chunk, FileTransfer{SrcPath: "/src.bin", DstPath: "/dst.bin"})
	require.NoError(t, err)
	assert.Equal(t, int64(16), readOffset)
	assert.Equal(t, int64(16), writeOffset)
	assert.Equal(t, src, wrote)
}

func TestExecute_Cancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var readCalls atomic.Int32
	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			readCalls.Add(1)
			return 0, nil
		},
	}, logrus.NewEntry(logrus.New()))

	err := w.Execute(ctx, Chunk{ID: "c1", Length: 8}, FileTransfer{SrcPath: "/src", DstPath: "/dst"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(0), readCalls.Load())
}

func TestExecute_SFTPReadError(t *testing.T) {
	t.Parallel()

	expected := errors.New("read failed")
	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			return 0, expected
		},
	}, logrus.NewEntry(logrus.New()))

	err := w.Execute(context.Background(), Chunk{ID: "c1", Length: 8}, FileTransfer{SrcPath: "/src", DstPath: "/dst"})
	require.Error(t, err)
	assert.ErrorIs(t, err, expected)
}

func TestExecute_SFTPWriteError(t *testing.T) {
	t.Parallel()

	expected := errors.New("write failed")
	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			copy(p, []byte("abcdefgh"))
			return 8, nil
		},
		writeFn: func(path string, p []byte, off int64) (int, error) {
			return 0, expected
		},
	}, logrus.NewEntry(logrus.New()))

	err := w.Execute(context.Background(), Chunk{ID: "c1", Length: 8}, FileTransfer{SrcPath: "/src", DstPath: "/dst"})
	require.Error(t, err)
	assert.ErrorIs(t, err, expected)
}

func TestExecute_PartialWrite(t *testing.T) {
	t.Parallel()

	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			copy(p, []byte("abcdefgh"))
			return 8, io.EOF
		},
		writeFn: func(path string, p []byte, off int64) (int, error) {
			return 4, nil
		},
	}, logrus.NewEntry(logrus.New()))

	err := w.Execute(context.Background(), Chunk{ID: "c1", Length: 8}, FileTransfer{SrcPath: "/src", DstPath: "/dst"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partial write")
}
