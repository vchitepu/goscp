//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"github.com/vchitepu/goscp/pkg/checkpoint"
	"github.com/vchitepu/goscp/pkg/transfer"
	"golang.org/x/crypto/ssh"
)

type r2rIntegrationClient struct {
	src *pkgsftp.Client
	dst *pkgsftp.Client
}

func (c *r2rIntegrationClient) ReadAt(path string, p []byte, off int64) (int, error) {
	resolved, side := parseR2RPath(path)
	if side != "src" {
		return 0, fmt.Errorf("read must use src path, got %q", path)
	}
	f, err := c.src.Open(resolved)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(p, off)
}

func (c *r2rIntegrationClient) WriteAt(path string, p []byte, off int64) (int, error) {
	resolved, side := parseR2RPath(path)
	if side != "dst" {
		return 0, fmt.Errorf("write must use dst path, got %q", path)
	}
	if err := c.dst.MkdirAll(filepath.Dir(resolved)); err != nil {
		return 0, err
	}
	f, err := c.dst.OpenFile(resolved, os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.WriteAt(p, off)
}

type r2rOpts struct {
	workers        int
	limitMbps      int
	chunkSize      uint64
	checkpointHome string
	resumeID       string
	onResult       func(transfer.Result)
}

func TestR2R_BasicCopy(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "basic-src.bin")
	dstPath := filepath.Join(dstRoot, "basic-dst.bin")
	require.NoError(t, writeSyntheticFile(srcPath, 6*1024*1024, 101))

	_, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{workers: 2, chunkSize: 1024 * 1024})
	require.NoError(t, err)
	require.Equal(t, sha256File(t, srcPath), sha256File(t, dstPath))
}

func TestR2R_DirectoryTree(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := filepath.Join(t.TempDir(), "src-tree")
	dstRoot := filepath.Join(t.TempDir(), "dst-tree")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "alpha", "beta"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "gamma"), 0o755))

	files := []struct {
		rel  string
		size uint64
		seed int64
	}{
		{rel: "root.txt", size: 32 * 1024, seed: 1},
		{rel: filepath.Join("alpha", "a.bin"), size: 128 * 1024, seed: 2},
		{rel: filepath.Join("alpha", "beta", "b.bin"), size: 2 * 1024 * 1024, seed: 3},
		{rel: filepath.Join("gamma", "c.log"), size: 96 * 1024, seed: 4},
		{rel: filepath.Join("gamma", "d.json"), size: 8 * 1024, seed: 5},
	}

	for _, f := range files {
		srcPath := filepath.Join(srcRoot, f.rel)
		require.NoError(t, writeSyntheticFile(srcPath, f.size, f.seed))
	}

	for _, f := range files {
		srcPath := filepath.Join(srcRoot, f.rel)
		dstPath := filepath.Join(dstRoot, f.rel)
		_, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{workers: 2, chunkSize: 256 * 1024})
		require.NoError(t, err)
	}

	for _, f := range files {
		srcPath := filepath.Join(srcRoot, f.rel)
		dstPath := filepath.Join(dstRoot, f.rel)
		require.Equal(t, sha256File(t, srcPath), sha256File(t, dstPath), "mismatch for %s", f.rel)
	}
}

func TestR2R_LargeFile(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "large-50mb.bin")
	dstPath := filepath.Join(dstRoot, "large-50mb.bin")
	require.NoError(t, writeSyntheticFile(srcPath, 50*1024*1024, 303))

	_, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{workers: 4, chunkSize: 4 * 1024 * 1024})
	require.NoError(t, err)
	require.Equal(t, sha256File(t, srcPath), sha256File(t, dstPath))
}

func TestR2R_ResumeAfterInterrupt(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	checkpointHome := t.TempDir()
	srcPath := filepath.Join(srcRoot, "resume-src-50mb.bin")
	dstPath := filepath.Join(dstRoot, "resume-dst-50mb.bin")
	require.NoError(t, writeSyntheticFile(srcPath, 50*1024*1024, 404))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fileSize := uint64(50 * 1024 * 1024)
	cancelAfter := fileSize / 5
	var doneBytes atomic.Uint64

	checkpointID, err := runR2RTransfer(ctx, client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{
		workers:        1,
		chunkSize:      5 * 1024 * 1024,
		limitMbps:      12,
		checkpointHome: checkpointHome,
		onResult: func(res transfer.Result) {
			if res.Err != nil {
				return
			}
			if doneBytes.Add(res.BytesTransferred) >= cancelAfter {
				cancel()
			}
		},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled"))
	require.NotEmpty(t, checkpointID)

	cp := checkpoint.NewFileCheckpointer(afero.NewOsFs(), checkpointHome)
	state, loadErr := cp.Load(checkpointID)
	require.NoError(t, loadErr)
	verifyCheckpointHasProgress(t, state)

	resumeID, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{
		workers:        1,
		chunkSize:      5 * 1024 * 1024,
		limitMbps:      12,
		checkpointHome: checkpointHome,
		resumeID:       checkpointID,
	})
	require.NoError(t, err)
	require.Equal(t, checkpointID, resumeID)
	require.Equal(t, sha256File(t, srcPath), sha256File(t, dstPath))
}

func TestR2R_SrcServerDown(t *testing.T) {
	srcSSH, rawStopSrc := startInProcessSSHServer(t)
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	var stopSrcOnce sync.Once
	stopSrc := func() { stopSrcOnce.Do(rawStopSrc) }
	defer stopSrc()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	checkpointHome := t.TempDir()
	srcPath := filepath.Join(srcRoot, "src-down.bin")
	dstPath := filepath.Join(dstRoot, "src-down.bin")
	require.NoError(t, writeSyntheticFile(srcPath, 24*1024*1024, 505))

	var stopped atomic.Bool
	checkpointID, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{
		workers:        1,
		chunkSize:      2 * 1024 * 1024,
		checkpointHome: checkpointHome,
		onResult: func(res transfer.Result) {
			if res.Err != nil {
				return
			}
			if stopped.CompareAndSwap(false, true) {
				stopSrc()
			}
		},
	})
	require.Error(t, err)
	require.NotEmpty(t, checkpointID)
	require.True(t, stopped.Load(), "source server should have been stopped mid-transfer")

	cp := checkpoint.NewFileCheckpointer(afero.NewOsFs(), checkpointHome)
	state, loadErr := cp.Load(checkpointID)
	require.NoError(t, loadErr)
	verifyCheckpointHasProgress(t, state)
}

func TestR2R_DstServerDown(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, rawStopDst := startInProcessSSHServer(t)
	defer dstSSH.Close()

	var stopDstOnce sync.Once
	stopDst := func() { stopDstOnce.Do(rawStopDst) }
	defer stopDst()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	checkpointHome := t.TempDir()
	srcPath := filepath.Join(srcRoot, "dst-down.bin")
	dstPath := filepath.Join(dstRoot, "dst-down.bin")
	require.NoError(t, writeSyntheticFile(srcPath, 24*1024*1024, 606))

	var stopped atomic.Bool
	checkpointID, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{
		workers:        1,
		chunkSize:      2 * 1024 * 1024,
		checkpointHome: checkpointHome,
		onResult: func(res transfer.Result) {
			if res.Err != nil {
				return
			}
			if stopped.CompareAndSwap(false, true) {
				stopDst()
			}
		},
	})
	require.Error(t, err)
	require.NotEmpty(t, checkpointID)
	require.True(t, stopped.Load(), "destination server should have been stopped mid-transfer")

	cp := checkpoint.NewFileCheckpointer(afero.NewOsFs(), checkpointHome)
	state, loadErr := cp.Load(checkpointID)
	require.NoError(t, loadErr)
	verifyCheckpointHasProgress(t, state)
}

func TestR2R_NetworkSlowdown(t *testing.T) {
	srcSSH, stopSrc := startInProcessSSHServer(t)
	defer stopSrc()
	defer srcSSH.Close()

	dstSSH, stopDst := startInProcessSSHServer(t)
	defer stopDst()
	defer dstSSH.Close()

	srcSFTP := mustNewRawSFTPClient(t, srcSSH)
	defer srcSFTP.Close()
	dstSFTP := mustNewRawSFTPClient(t, dstSSH)
	defer dstSFTP.Close()

	client := &r2rIntegrationClient{src: srcSFTP, dst: dstSFTP}
	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "slow-src.bin")
	dstPath := filepath.Join(dstRoot, "slow-dst.bin")

	size := uint64(20 * 1024 * 1024)
	limitMbps := 10
	require.NoError(t, writeSyntheticFile(srcPath, size, 707))

	start := time.Now()
	_, err := runR2RTransfer(context.Background(), client, logicalR2RSrc(srcPath), logicalR2RDst(dstPath), r2rOpts{
		workers:   1,
		chunkSize: 1024 * 1024,
		limitMbps: limitMbps,
	})
	require.NoError(t, err)
	elapsed := time.Since(start)

	bytesPerSec := float64(limitMbps * 125000)
	expectedSeconds := math.Max(0, (float64(size)-bytesPerSec)/bytesPerSec)
	require.GreaterOrEqual(t, elapsed.Seconds(), expectedSeconds*0.8, "transfer finished too quickly for configured rate limit")
	require.Equal(t, sha256File(t, srcPath), sha256File(t, dstPath))
}

func runR2RTransfer(ctx context.Context, client transfer.SFTPClient, srcPath, dstPath string, opts r2rOpts) (string, error) {
	size, err := logicalFileSize(strings.TrimPrefix(srcPath, "src:"))
	if err != nil {
		return "", err
	}

	workers := opts.workers
	if workers <= 0 {
		workers = 1
	}
	chunkCfg := transfer.ChunkConfig{NumWorkers: workers}
	if opts.chunkSize > 0 {
		chunkCfg.MinSize = opts.chunkSize
		chunkCfg.MaxSize = opts.chunkSize
	}

	chunker := transfer.NewDefaultFileChunker()
	chunks, err := chunker.Chunk(transfer.FileInfo{ID: "r2r-file", Size: size}, chunkCfg)
	if err != nil {
		return "", err
	}
	for i := range chunks {
		chunks[i].ID = fmt.Sprintf("r2r-file#%d", i+1)
		chunks[i].FileID = "r2r-file"
	}

	checkpointID := opts.resumeID
	cpEnabled := opts.checkpointHome != ""
	var cp *checkpoint.FileCheckpointer
	if cpEnabled {
		cp = checkpoint.NewFileCheckpointer(afero.NewOsFs(), opts.checkpointHome)
	}

	if cpEnabled {
		if checkpointID == "" {
			created := time.Now().UTC()
			state := checkpoint.CheckpointState{
				ID:        checkpoint.ComputeCheckpointID(checkpoint.TransferSpec{SrcPaths: []string{srcPath}, DstPath: dstPath}, created),
				CreatedAt: created,
				Spec:      checkpoint.TransferSpec{SrcPaths: []string{srcPath}, DstPath: dstPath},
				Chunks:    append([]transfer.Chunk(nil), chunks...),
			}
			if err := cp.Save(state); err != nil {
				return "", err
			}
			checkpointID = state.ID
		} else {
			state, err := cp.Load(checkpointID)
			if err != nil {
				return checkpointID, err
			}
			byID := make(map[string]transfer.Chunk, len(state.Chunks))
			for _, ch := range state.Chunks {
				byID[ch.ID] = ch
			}
			pending := make([]transfer.Chunk, 0, len(chunks))
			for _, ch := range chunks {
				if existing, ok := byID[ch.ID]; ok {
					if existing.State == transfer.ChunkDone {
						continue
					}
					ch = existing
				}
				pending = append(pending, ch)
			}
			chunks = pending
		}
	}

	scheduler := transfer.NewDefaultScheduler(transfer.SchedulerConfig{NumWorkers: workers, LimitMbps: opts.limitMbps})
	spec := transfer.FileTransfer{SrcPath: srcPath, DstPath: dstPath, Direction: transfer.R2R}
	logger := logrus.NewEntry(logrus.New())
	factory := integrationWorkerFactory{client: client, logger: logger}

	results := scheduler.Schedule(ctx, chunks, spec, factory)
	var firstErr error
	for result := range results {
		if opts.onResult != nil {
			opts.onResult(result)
		}
		if result.Err != nil && firstErr == nil {
			firstErr = result.Err
		}
		if cpEnabled && result.ChunkID != "" {
			state := transfer.ChunkFailed
			done := uint64(0)
			if result.Err == nil {
				state = transfer.ChunkDone
				done = result.BytesTransferred
			}
			if existing, ok := chunkByID(chunks, result.ChunkID); ok {
				existing.Done = done
				existing.State = state
				_ = cp.UpdateChunk(checkpointID, existing)
			}
		}
	}

	if firstErr != nil {
		return checkpointID, firstErr
	}
	return checkpointID, nil
}

func mustNewRawSFTPClient(t *testing.T, sshClient *ssh.Client) *pkgsftp.Client {
	t.Helper()
	client, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	return client
}

func parseR2RPath(path string) (resolved string, side string) {
	if strings.HasPrefix(path, "src:") {
		return strings.TrimPrefix(path, "src:"), "src"
	}
	if strings.HasPrefix(path, "dst:") {
		return strings.TrimPrefix(path, "dst:"), "dst"
	}
	return path, ""
}

func logicalR2RSrc(path string) string { return "src:" + path }
func logicalR2RDst(path string) string { return "dst:" + path }

func verifyCheckpointHasProgress(t *testing.T, state checkpoint.CheckpointState) {
	t.Helper()
	require.NotEmpty(t, state.Chunks)
	doneCount := 0
	incompleteCount := 0
	for _, ch := range state.Chunks {
		if ch.State == transfer.ChunkDone {
			doneCount++
		} else {
			incompleteCount++
		}
	}
	require.Greater(t, doneCount, 0, "checkpoint should preserve completed chunk progress")
	require.Greater(t, incompleteCount, 0, "checkpoint should preserve unfinished work for resume")
}
