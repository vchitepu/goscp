package transfer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gssh "golang.org/x/crypto/ssh"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type workerFactoryFunc func(srcConn *gssh.Client, dstConn *gssh.Client, direction TransferDirection) (TransferWorker, error)

func (f workerFactoryFunc) New(srcConn *gssh.Client, dstConn *gssh.Client, direction TransferDirection) (TransferWorker, error) {
	return f(srcConn, dstConn, direction)
}

func collectResults(ch <-chan Result) []Result {
	results := make([]Result, 0)
	for r := range ch {
		results = append(results, r)
	}
	return results
}

func TestSchedule_AllChunks(t *testing.T) {
	t.Parallel()

	chunks := []Chunk{{ID: "c1", Length: 4}, {ID: "c2", Length: 8}, {ID: "c3", Length: 16}}
	w := &MockTransferWorker{}
	w.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 2})
	results := collectResults(s.Schedule(context.Background(), chunks, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return w, nil
	})))

	require.Len(t, results, len(chunks))
	for _, r := range results {
		assert.NoError(t, r.Err)
		assert.NotEmpty(t, r.ChunkID)
	}
	w.AssertNumberOfCalls(t, "Execute", len(chunks))
}

func TestSchedule_Cancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	chunks := []Chunk{{ID: "c1", Length: 8}, {ID: "c2", Length: 8}, {ID: "c3", Length: 8}}
	w := &MockTransferWorker{}
	w.On("Execute", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		time.Sleep(30 * time.Millisecond)
	}).Return(nil)

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1})
	resCh := s.Schedule(ctx, chunks, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return w, nil
	}))
	time.AfterFunc(10*time.Millisecond, cancel)

	results := collectResults(resCh)
	require.NotEmpty(t, results)
	cancelled := false
	for _, r := range results {
		if errors.Is(r.Err, context.Canceled) {
			cancelled = true
		}
	}
	assert.True(t, cancelled)
}

func TestSchedule_WorkerError(t *testing.T) {
	t.Parallel()

	expected := errors.New("boom")
	chunks := []Chunk{{ID: "c1", Length: 8}}
	w := &MockTransferWorker{}
	w.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(expected).Once()

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1})
	results := collectResults(s.Schedule(context.Background(), chunks, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return w, nil
	})))

	require.Len(t, results, 1)
	assert.ErrorIs(t, results[0].Err, expected)
}

func TestSchedule_RateLimit(t *testing.T) {
	t.Parallel()

	chunks := []Chunk{{ID: "c1", Length: 256 * 1024}}
	w := &MockTransferWorker{}
	w.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1, LimitMbps: 1})
	start := time.Now()
	results := collectResults(s.Schedule(context.Background(), chunks, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return w, nil
	})))
	elapsed := time.Since(start)

	require.Len(t, results, 1)
	assert.NoError(t, results[0].Err)
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond)
}

func TestSchedule_Parallel(t *testing.T) {
	t.Parallel()

	chunks := []Chunk{{ID: "c1", Length: 1}, {ID: "c2", Length: 1}}
	var active atomic.Int32
	var maxActive atomic.Int32
	worker := &MockTransferWorker{}
	worker.On("Execute", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		cur := active.Add(1)
		for {
			max := maxActive.Load()
			if cur <= max || maxActive.CompareAndSwap(max, cur) {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
		active.Add(-1)
	}).Return(nil)

	var mu sync.Mutex
	created := 0
	factory := workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		mu.Lock()
		defer mu.Unlock()
		created++
		return worker, nil
	})

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 2})
	start := time.Now()
	results := collectResults(s.Schedule(context.Background(), chunks, FileTransfer{Direction: L2R}, factory))
	elapsed := time.Since(start)

	require.Len(t, results, 2)
	assert.GreaterOrEqual(t, created, 2)
	assert.GreaterOrEqual(t, maxActive.Load(), int32(2))
	assert.Less(t, elapsed, 280*time.Millisecond)
}
