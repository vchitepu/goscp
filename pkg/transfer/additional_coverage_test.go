package transfer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	gssh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

type acquireErrPool struct{ err error }

func (p acquireErrPool) Acquire(context.Context, TransferDirection) (*gssh.Client, *gssh.Client, error) {
	return nil, nil, p.err
}
func (acquireErrPool) Release(*gssh.Client, *gssh.Client) {}

type releaseTrackPool struct {
	releaseCalls int
	src          *gssh.Client
	dst          *gssh.Client
}

func (p *releaseTrackPool) Acquire(context.Context, TransferDirection) (*gssh.Client, *gssh.Client, error) {
	p.src = &gssh.Client{}
	p.dst = &gssh.Client{}
	return p.src, p.dst, nil
}
func (p *releaseTrackPool) Release(src, dst *gssh.Client) {
	if src == p.src && dst == p.dst {
		p.releaseCalls++
	}
}

func TestWorkerExecute_ZeroLength(t *testing.T) {
	t.Parallel()

	called := false
	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			called = true
			return 0, nil
		},
	}, nil)

	err := w.Execute(context.Background(), Chunk{ID: "z", Length: 0}, FileTransfer{SrcPath: "/s", DstPath: "/d"})
	require.NoError(t, err)
	assert.False(t, called)
}

func TestWorkerExecute_ReadZeroBytes(t *testing.T) {
	t.Parallel()

	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			return 0, nil
		},
	}, nil)

	err := w.Execute(context.Background(), Chunk{ID: "z", Offset: 7, Length: 8}, FileTransfer{SrcPath: "/s", DstPath: "/d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no bytes read")
}

func TestWorkerExecute_ContextCancelledAfterRead(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	w := NewDefaultTransferWorker(&workerFakeSFTP{
		readFn: func(path string, p []byte, off int64) (int, error) {
			copy(p, []byte("abcdefgh"))
			cancel()
			return 8, nil
		},
		writeFn: func(path string, p []byte, off int64) (int, error) {
			return len(p), nil
		},
	}, nil)

	err := w.Execute(ctx, Chunk{ID: "z", Length: 8}, FileTransfer{SrcPath: "/s", DstPath: "/d"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewDefaultScheduler_DefaultWorkerCount(t *testing.T) {
	t.Parallel()

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 0})
	require.NotNil(t, s)
	assert.Equal(t, 1, s.cfg.NumWorkers)
}

func TestSchedule_FactoryError(t *testing.T) {
	t.Parallel()

	expected := errors.New("factory failed")
	chunks := []Chunk{{ID: "c1", Length: 1}, {ID: "c2", Length: 1}}
	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1})
	results := collectResults(s.Schedule(context.Background(), chunks, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return nil, expected
	})))

	require.NotEmpty(t, results)
	assert.ErrorIs(t, results[0].Err, expected)
}

func TestSchedule_PoolAcquireError(t *testing.T) {
	t.Parallel()

	expected := errors.New("acquire failed")
	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1}, acquireErrPool{err: expected})
	results := collectResults(s.Schedule(context.Background(), []Chunk{{ID: "c1", Length: 1}}, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return &MockTransferWorker{}, nil
	})))

	require.NotEmpty(t, results)
	assert.ErrorIs(t, results[0].Err, expected)
}

func TestSchedule_PoolReleaseCalled(t *testing.T) {
	t.Parallel()

	p := &releaseTrackPool{}
	worker := &MockTransferWorker{}
	worker.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	s := NewDefaultScheduler(SchedulerConfig{NumWorkers: 1}, p)
	results := collectResults(s.Schedule(context.Background(), []Chunk{{ID: "c1", Length: 1}}, FileTransfer{Direction: R2R}, workerFactoryFunc(func(src *gssh.Client, dst *gssh.Client, direction TransferDirection) (TransferWorker, error) {
		assert.Equal(t, p.src, src)
		assert.Equal(t, p.dst, dst)
		assert.Equal(t, R2R, direction)
		return worker, nil
	})))

	require.Len(t, results, 1)
	assert.NoError(t, results[0].Err)
	assert.Equal(t, 1, p.releaseCalls)
}

func TestWaitForBytes_CanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	limiter := rate.NewLimiter(rate.Limit(1), 1)

	err := waitForBytes(ctx, limiter, 10)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestMockTransferScheduler_Schedule(t *testing.T) {
	t.Parallel()

	m := &MockTransferScheduler{}
	out := make(chan Result, 1)
	out <- Result{ChunkID: "c1", BytesTransferred: 10}
	close(out)

	m.On("Schedule", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((<-chan Result)(out)).Once()
	got := m.Schedule(context.Background(), []Chunk{{ID: "c1", Length: 10}}, FileTransfer{Direction: L2R}, workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return &MockTransferWorker{}, nil
	}))

	res := collectResults(got)
	require.Len(t, res, 1)
	assert.Equal(t, "c1", res[0].ChunkID)
	assert.Equal(t, uint64(10), res[0].BytesTransferred)
	m.AssertExpectations(t)
}
