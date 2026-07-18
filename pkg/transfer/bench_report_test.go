package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBenchmarkSanity(t *testing.T) {
	t.Parallel()

	chunks := makeFileChunks("sanity-workers", 1*1024*1024*1024, 8)
	singleElapsed, err := runSchedulerScenario(context.Background(), 1, chunks, benchmarkDefaultLatency, benchmarkDefaultThroughput)
	require.NoError(t, err)

	multiElapsed, err := runSchedulerScenario(context.Background(), 4, chunks, benchmarkDefaultLatency, benchmarkDefaultThroughput)
	require.NoError(t, err)

	// 20% tolerance: multi-worker should beat single-worker by at least 20%.
	require.Lessf(t, multiElapsed, time.Duration(float64(singleElapsed)*0.80), "expected 4 workers faster than 1 worker by >=20%%, single=%s multi=%s", singleElapsed, multiElapsed)
}

func TestChunkSizeSanity(t *testing.T) {
	t.Parallel()

	largeChunks := makeFileChunks("large-128mb", 1*1024*1024*1024, 8) // 128MB chunks
	smallChunks := makeFileChunks("small-16mb", 1*1024*1024*1024, 64) // 16MB chunks

	largeElapsed, err := runSchedulerScenario(context.Background(), 1, largeChunks, benchmarkDefaultLatency, benchmarkDefaultThroughput)
	require.NoError(t, err)

	smallElapsed, err := runSchedulerScenario(context.Background(), 1, smallChunks, benchmarkDefaultLatency, benchmarkDefaultThroughput)
	require.NoError(t, err)

	// 20% tolerance: larger chunks should be measurably faster from fewer round trips.
	require.Lessf(t, largeElapsed, time.Duration(float64(smallElapsed)*0.80), "expected 128MB chunks faster than 16MB chunks by >=20%%, large=%s small=%s", largeElapsed, smallElapsed)
}
