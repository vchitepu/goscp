package transfer

import (
	"context"
	"fmt"
	"testing"
	"time"

	gssh "golang.org/x/crypto/ssh"
)

const (
	benchmarkDefaultLatency    = 100 * time.Millisecond
	benchmarkDefaultThroughput = 200 * 1024 * 1024 // 200MB/s
)

// MockSFTPClient is a no-op SFTP client used by benchmark workers.
type MockSFTPClient struct{}

func (m *MockSFTPClient) ReadAt(path string, p []byte, off int64) (int, error) {
	return len(p), nil
}

func (m *MockSFTPClient) WriteAt(path string, p []byte, off int64) (int, error) {
	return len(p), nil
}

// simulatedMockTransferWorker emulates transfer latency + throughput without SSH/SFTP I/O.
type simulatedMockTransferWorker struct {
	sftp                  SFTPClient
	perChunkLatency       time.Duration
	throughputBytesPerSec float64
}

func (w *simulatedMockTransferWorker) Execute(ctx context.Context, chunk Chunk, _ FileTransfer) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Touch the mock SFTP client so benchmark path always uses mock infrastructure.
	if w.sftp != nil {
		buf := []byte{0}
		if _, err := w.sftp.ReadAt("/src/mock", buf, int64(chunk.Offset)); err != nil {
			return err
		}
		if _, err := w.sftp.WriteAt("/dst/mock", buf, int64(chunk.Offset)); err != nil {
			return err
		}
	}

	if w.throughputBytesPerSec <= 0 {
		return fmt.Errorf("invalid throughput: %f", w.throughputBytesPerSec)
	}
	transferDuration := time.Duration(float64(chunk.Length) / w.throughputBytesPerSec * float64(time.Second))
	wait := w.perChunkLatency + transferDuration

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func makeFileChunks(fileID string, totalBytes uint64, pieceCount int) []Chunk {
	if pieceCount <= 0 {
		return nil
	}

	chunks := make([]Chunk, 0, pieceCount)
	baseSize := totalBytes / uint64(pieceCount)
	remainder := totalBytes % uint64(pieceCount)
	var offset uint64

	for i := 0; i < pieceCount; i++ {
		size := baseSize
		if uint64(i) < remainder {
			size++
		}
		chunks = append(chunks, Chunk{
			ID:     fmt.Sprintf("%s-%03d", fileID, i),
			FileID: fileID,
			Offset: offset,
			Length: size,
			State:  ChunkPending,
		})
		offset += size
	}

	return chunks
}

func makeSmallFileChunks(fileCount int, fileSize uint64) []Chunk {
	chunks := make([]Chunk, 0, fileCount)
	for i := 0; i < fileCount; i++ {
		fileID := fmt.Sprintf("small-%03d", i)
		chunks = append(chunks, Chunk{
			ID:     fmt.Sprintf("%s-000", fileID),
			FileID: fileID,
			Offset: 0,
			Length: fileSize,
			State:  ChunkPending,
		})
	}
	return chunks
}

func chunkBytes(chunks []Chunk) uint64 {
	var total uint64
	for _, c := range chunks {
		total += c.Length
	}
	return total
}

func runSchedulerScenario(ctx context.Context, workerCount int, chunks []Chunk, perChunkLatency time.Duration, throughputBytesPerSec float64) (time.Duration, error) {
	scheduler := NewDefaultScheduler(SchedulerConfig{NumWorkers: workerCount})
	mockSFTP := &MockSFTPClient{}
	factory := workerFactoryFunc(func(_ *gssh.Client, _ *gssh.Client, _ TransferDirection) (TransferWorker, error) {
		return &simulatedMockTransferWorker{
			sftp:                  mockSFTP,
			perChunkLatency:       perChunkLatency,
			throughputBytesPerSec: throughputBytesPerSec,
		}, nil
	})

	started := time.Now()
	results := collectResults(scheduler.Schedule(ctx, chunks, FileTransfer{Direction: L2R}, factory))
	elapsed := time.Since(started)

	for _, result := range results {
		if result.Err != nil {
			return elapsed, result.Err
		}
	}
	return elapsed, nil
}

func benchmarkScheduler(b *testing.B, workerCount int, chunks []Chunk) {
	b.Helper()
	b.ReportAllocs()
	b.SetBytes(int64(chunkBytes(chunks)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := runSchedulerScenario(context.Background(), workerCount, chunks, benchmarkDefaultLatency, benchmarkDefaultThroughput); err != nil {
			b.Fatalf("benchmark run failed: %v", err)
		}
	}
}

func BenchmarkScheduler_SingleWorker(b *testing.B) {
	chunks := makeFileChunks("single-worker", 1*1024*1024*1024, 8)
	benchmarkScheduler(b, 1, chunks)
}

func BenchmarkScheduler_4Workers(b *testing.B) {
	chunks := makeFileChunks("four-workers", 1*1024*1024*1024, 8)
	benchmarkScheduler(b, 4, chunks)
}

func BenchmarkScheduler_8Workers(b *testing.B) {
	chunks := makeFileChunks("eight-workers", 1*1024*1024*1024, 8)
	benchmarkScheduler(b, 8, chunks)
}

func BenchmarkScheduler_16Workers(b *testing.B) {
	chunks := makeFileChunks("sixteen-workers", 1*1024*1024*1024, 8)
	benchmarkScheduler(b, 16, chunks)
}

func BenchmarkScheduler_SmallFiles(b *testing.B) {
	chunks := makeSmallFileChunks(100, 10*1024*1024)
	benchmarkScheduler(b, 4, chunks)
}

func BenchmarkScheduler_LargeChunks(b *testing.B) {
	chunks := makeFileChunks("large-chunks", 1*1024*1024*1024, 2)
	benchmarkScheduler(b, 2, chunks)
}

func BenchmarkScheduler_SmallChunks(b *testing.B) {
	chunks := makeFileChunks("small-chunks", 1*1024*1024*1024, 64)
	benchmarkScheduler(b, 8, chunks)
}
