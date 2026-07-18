package transfer

import (
	"context"
	"sync"

	gssh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

// DefaultScheduler is a worker-pool scheduler implementation.
type DefaultScheduler struct {
	cfg  SchedulerConfig
	pool SSHDialerPool
}

// NewDefaultScheduler creates a DefaultScheduler.
func NewDefaultScheduler(cfg SchedulerConfig, pools ...SSHDialerPool) *DefaultScheduler {
	if cfg.NumWorkers <= 0 {
		cfg.NumWorkers = 1
	}

	var pool SSHDialerPool
	if len(pools) > 0 {
		pool = pools[0]
	}

	return &DefaultScheduler{cfg: cfg, pool: pool}
}

func (s *DefaultScheduler) Schedule(ctx context.Context, chunks []Chunk, spec FileTransfer, factory WorkerFactory) <-chan Result {
	resultCh := make(chan Result, len(chunks))
	workCh := make(chan Chunk, len(chunks))

	var limiter *rate.Limiter
	if s.cfg.LimitMbps > 0 {
		bytesPerSec := s.cfg.LimitMbps * 125000
		if bytesPerSec <= 0 {
			bytesPerSec = 1
		}
		limiter = rate.NewLimiter(rate.Limit(bytesPerSec), bytesPerSec)
	}

	go func() {
		defer close(workCh)
		for i, chunk := range chunks {
			select {
			case <-ctx.Done():
				resultCh <- Result{ChunkID: chunk.ID, Err: ctx.Err()}
				for _, rem := range chunks[i+1:] {
					resultCh <- Result{ChunkID: rem.ID, Err: ctx.Err()}
				}
				return
			case workCh <- chunk:
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(s.cfg.NumWorkers)
	for i := 0; i < s.cfg.NumWorkers; i++ {
		go func() {
			defer wg.Done()

			var srcConn *gssh.Client
			var dstConn *gssh.Client
			if s.pool != nil {
				acquiredSrc, acquiredDst, err := s.pool.Acquire(ctx, spec.Direction)
				if err != nil {
					resultCh <- Result{Err: err}
					for range workCh {
					}
					return
				}
				srcConn = acquiredSrc
				dstConn = acquiredDst
				defer s.pool.Release(acquiredSrc, acquiredDst)
			}

			worker, err := factory.New(srcConn, dstConn, spec.Direction)
			if err != nil {
				resultCh <- Result{Err: err}
				for range workCh {
				}
				return
			}

			for chunk := range workCh {
				if err := ctx.Err(); err != nil {
					resultCh <- Result{ChunkID: chunk.ID, Err: err}
					continue
				}
				if limiter != nil {
					if err := waitForBytes(ctx, limiter, chunk.Length); err != nil {
						resultCh <- Result{ChunkID: chunk.ID, Err: err}
						continue
					}
				}

				err := worker.Execute(ctx, chunk, spec)
				if err != nil {
					resultCh <- Result{ChunkID: chunk.ID, Err: err}
					continue
				}
				resultCh <- Result{ChunkID: chunk.ID, BytesTransferred: chunk.Length}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	return resultCh
}

func waitForBytes(ctx context.Context, limiter *rate.Limiter, n uint64) error {
	remaining := n
	burst := limiter.Burst()
	if burst <= 0 {
		burst = 1
	}
	for remaining > 0 {
		step := uint64(burst)
		if remaining < step {
			step = remaining
		}
		if err := limiter.WaitN(ctx, int(step)); err != nil {
			return err
		}
		remaining -= step
	}
	return nil
}
