package progress

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger(out io.Writer) *logrus.Entry {
	l := logrus.New()
	l.SetOutput(out)
	l.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true, DisableColors: true})
	return logrus.NewEntry(l)
}

func TestUpdate_IncrementsBytesTransferred(t *testing.T) {
	t.Parallel()

	r := NewMultiBarReporterWithOutput(newTestLogger(io.Discard), io.Discard)
	f := FileInfo{ID: "file-1", Size: 100}
	r.Add(f)
	r.Update(Result{ChunkID: "file-1", BytesTransferred: 40})

	r.mu.Lock()
	defer r.mu.Unlock()
	require.Equal(t, uint64(40), r.transferredBytes)
	fp, ok := r.files["file-1"]
	require.True(t, ok)
	require.Equal(t, uint64(40), fp.transferred)
}

func TestComplete_MarksAllDone(t *testing.T) {
	t.Parallel()

	r := NewMultiBarReporterWithOutput(newTestLogger(io.Discard), io.Discard)
	r.Add(FileInfo{ID: "file-1", Size: 100})
	r.Add(FileInfo{ID: "file-2", Size: 200})

	r.Complete()

	r.mu.Lock()
	defer r.mu.Unlock()
	require.True(t, r.completed)
	require.True(t, r.files["file-1"].done)
	require.True(t, r.files["file-2"].done)
}

func TestFail_MarksFileFailed(t *testing.T) {
	t.Parallel()

	r := NewMultiBarReporterWithOutput(newTestLogger(io.Discard), io.Discard)
	f := FileInfo{ID: "file-1", Size: 100}
	r.Add(f)

	expectedErr := errors.New("write failed")
	r.Fail(f, expectedErr)

	r.mu.Lock()
	defer r.mu.Unlock()
	require.True(t, r.files["file-1"].failed)
	require.True(t, r.files["file-1"].done)
	require.ErrorIs(t, r.files["file-1"].err, expectedErr)
}

func TestQuietReporter_NoTerminalOutput(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := newTestLogger(&logBuf)
	q := NewQuietReporter(logger)

	q.Add(FileInfo{ID: "file-1", Size: 100})
	q.Update(Result{ChunkID: "chunk-1", BytesTransferred: 25})
	q.Fail(FileInfo{ID: "file-1", Size: 100}, errors.New("boom"))
	q.Complete()

	assert.Contains(t, logBuf.String(), "quiet reporter")
}

func TestLogReporter_EmitsCurrentFileAndPercent(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	r := NewLogReporter(newTestLogger(&logBuf))

	r.Add(FileInfo{ID: "file-1", Size: 100})
	r.Update(Result{ChunkID: "file-1", BytesTransferred: 40})

	out := logBuf.String()
	assert.Contains(t, out, "event=file_start")
	assert.Contains(t, out, "event=file_progress")
	assert.Contains(t, out, "current_file=file-1")
	assert.Contains(t, out, "percent_complete=40")
}
