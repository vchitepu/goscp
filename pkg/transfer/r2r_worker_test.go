package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type r2rFakeSFTPClient struct {
	openFn   func(path string) (io.ReadCloser, error)
	createFn func(path string) (io.WriteCloser, error)
}

func (f *r2rFakeSFTPClient) Open(path string) (io.ReadCloser, error) {
	if f.openFn == nil {
		return nil, nil
	}
	return f.openFn(path)
}

func (f *r2rFakeSFTPClient) Create(path string) (io.WriteCloser, error) {
	if f.createFn == nil {
		return nil, nil
	}
	return f.createFn(path)
}

func (f *r2rFakeSFTPClient) Stat(path string) (os.FileInfo, error) { return nil, nil }
func (f *r2rFakeSFTPClient) ReadDir(path string) ([]os.FileInfo, error) {
	return nil, nil
}
func (f *r2rFakeSFTPClient) MkdirAll(path string, perm os.FileMode) error { return nil }
func (f *r2rFakeSFTPClient) Close() error                                { return nil }

type testReadSeekCloser struct {
	data      []byte
	pos       int64
	calls     int
	maxRead   int
	failCall  int
	failError error
}

func (r *testReadSeekCloser) Read(p []byte) (int, error) {
	r.calls++
	if len(p) > r.maxRead {
		r.maxRead = len(p)
	}
	if r.failCall > 0 && r.calls == r.failCall {
		return 0, r.failError
	}
	if r.pos >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += int64(n)
	if r.pos >= int64(len(r.data)) {
		return n, io.EOF
	}
	return n, nil
}

func (r *testReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		r.pos = offset
	case io.SeekCurrent:
		r.pos += offset
	case io.SeekEnd:
		r.pos = int64(len(r.data)) + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if r.pos < 0 {
		r.pos = 0
	}
	return r.pos, nil
}

func (r *testReadSeekCloser) Close() error { return nil }

type testWriteSeekCloser struct {
	data      []byte
	pos       int64
	calls     int
	maxWrite  int
	total     int
	failCall  int
	failError error
	onWrite   func(call int)
}

func (w *testWriteSeekCloser) Write(p []byte) (int, error) {
	w.calls++
	if len(p) > w.maxWrite {
		w.maxWrite = len(p)
	}
	if w.onWrite != nil {
		w.onWrite(w.calls)
	}
	if w.failCall > 0 && w.calls == w.failCall {
		return 0, w.failError
	}

	end := int(w.pos) + len(p)
	if end > len(w.data) {
		newBuf := make([]byte, end)
		copy(newBuf, w.data)
		w.data = newBuf
	}
	copy(w.data[w.pos:], p)
	w.pos += int64(len(p))
	w.total += len(p)
	return len(p), nil
}

func (w *testWriteSeekCloser) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		w.pos = offset
	case io.SeekCurrent:
		w.pos += offset
	case io.SeekEnd:
		w.pos = int64(len(w.data)) + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if w.pos < 0 {
		w.pos = 0
	}
	return w.pos, nil
}

func (w *testWriteSeekCloser) Close() error { return nil }

func TestR2RExecute_Success(t *testing.T) {
	t.Parallel()

	reader := &testReadSeekCloser{data: []byte("0123456789")}
	writer := &testWriteSeekCloser{data: make([]byte, 10)}

	worker := NewR2RTransferWorker(
		&r2rFakeSFTPClient{openFn: func(path string) (io.ReadCloser, error) {
			assert.Equal(t, "/src/file.bin", path)
			return reader, nil
		}},
		&r2rFakeSFTPClient{createFn: func(path string) (io.WriteCloser, error) {
			assert.Equal(t, "/dst/file.bin", path)
			return writer, nil
		}},
		logrus.NewEntry(logrus.New()),
	)

	err := worker.Execute(context.Background(), Chunk{ID: "c1", Offset: 3, Length: 4}, FileTransfer{
		SrcPath:   "/src/file.bin",
		DstPath:   "/dst/file.bin",
		SrcHost:   "src-host",
		DstHost:   "dst-host",
		Direction: R2R,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("3456"), writer.data[3:7])
}

func TestR2RExecute_ReadError(t *testing.T) {
	t.Parallel()

	expected := errors.New("read failed")
	reader := &testReadSeekCloser{data: []byte("abc"), failCall: 1, failError: expected}
	writer := &testWriteSeekCloser{}

	worker := NewR2RTransferWorker(
		&r2rFakeSFTPClient{openFn: func(path string) (io.ReadCloser, error) { return reader, nil }},
		&r2rFakeSFTPClient{createFn: func(path string) (io.WriteCloser, error) { return writer, nil }},
		logrus.NewEntry(logrus.New()),
	)

	err := worker.Execute(context.Background(), Chunk{ID: "c1", Offset: 0, Length: 3}, FileTransfer{SrcPath: "/src", DstPath: "/dst", Direction: R2R})
	require.Error(t, err)
	assert.ErrorIs(t, err, expected)
}

func TestR2RExecute_WriteError(t *testing.T) {
	t.Parallel()

	expected := errors.New("write failed")
	reader := &testReadSeekCloser{data: []byte("abcdef")}
	writer := &testWriteSeekCloser{failCall: 1, failError: expected}

	worker := NewR2RTransferWorker(
		&r2rFakeSFTPClient{openFn: func(path string) (io.ReadCloser, error) { return reader, nil }},
		&r2rFakeSFTPClient{createFn: func(path string) (io.WriteCloser, error) { return writer, nil }},
		logrus.NewEntry(logrus.New()),
	)

	err := worker.Execute(context.Background(), Chunk{ID: "c1", Offset: 0, Length: 6}, FileTransfer{SrcPath: "/src", DstPath: "/dst", Direction: R2R})
	require.Error(t, err)
	assert.ErrorIs(t, err, expected)
}

func TestR2RExecute_Cancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	data := bytes.Repeat([]byte("x"), 6*1024*1024)
	reader := &testReadSeekCloser{data: data}
	writer := &testWriteSeekCloser{onWrite: func(call int) {
		if call == 1 {
			cancel()
		}
	}}

	worker := NewR2RTransferWorker(
		&r2rFakeSFTPClient{openFn: func(path string) (io.ReadCloser, error) { return reader, nil }},
		&r2rFakeSFTPClient{createFn: func(path string) (io.WriteCloser, error) { return writer, nil }},
		logrus.NewEntry(logrus.New()),
	)

	err := worker.Execute(ctx, Chunk{ID: "c1", Offset: 0, Length: uint64(len(data))}, FileTransfer{SrcPath: "/src", DstPath: "/dst", Direction: R2R})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.GreaterOrEqual(t, writer.calls, 1)
}

func TestR2RExecute_LargeChunk(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("a"), 10*1024*1024)
	reader := &testReadSeekCloser{data: data}
	writer := &testWriteSeekCloser{}

	worker := NewR2RTransferWorker(
		&r2rFakeSFTPClient{openFn: func(path string) (io.ReadCloser, error) { return reader, nil }},
		&r2rFakeSFTPClient{createFn: func(path string) (io.WriteCloser, error) { return writer, nil }},
		logrus.NewEntry(logrus.New()),
	)

	err := worker.Execute(context.Background(), Chunk{ID: "c1", Offset: 0, Length: uint64(len(data))}, FileTransfer{SrcPath: "/src", DstPath: "/dst", Direction: R2R})
	require.NoError(t, err)

	assert.LessOrEqual(t, reader.maxRead, 4*1024*1024)
	assert.Greater(t, reader.calls, 1)
	assert.Equal(t, len(data), writer.total)
}
