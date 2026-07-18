//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
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

type integrationClient struct {
	remote *pkgsftp.Client
}

func (c *integrationClient) ReadAt(path string, p []byte, off int64) (int, error) {
	resolved, remote := parseLogicalPath(path)
	if remote {
		f, err := c.remote.Open(resolved)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return f.ReadAt(p, off)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(p, off)
}

func (c *integrationClient) WriteAt(path string, p []byte, off int64) (int, error) {
	resolved, remote := parseLogicalPath(path)
	if remote {
		if err := c.remote.MkdirAll(filepath.Dir(resolved)); err != nil {
			return 0, err
		}
		f, err := c.remote.OpenFile(resolved, os.O_CREATE|os.O_WRONLY)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return f.WriteAt(p, off)
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.WriteAt(p, off)
}

type transferOpts struct {
	workers        int
	limitMbps      int
	chunkSize      uint64
	checkpointHome string
	resumeID       string
}

func TestIntegration_LocalToRemote(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	src := filepath.Join(localRoot, "local-to-remote.bin")
	dst := filepath.Join(remoteRoot, "local-to-remote.bin")
	require.NoError(t, writeSyntheticFile(src, 3*1024*1024, 7))

	_, err = runFileTransfer(context.Background(), client, logicalLocal(src), logicalRemote(dst), transferOpts{workers: 2, chunkSize: 1024 * 1024})
	require.NoError(t, err)

	srcHash := sha256File(t, src)
	dstHash := sha256File(t, dst)
	require.Equal(t, srcHash, dstHash)
}

func TestIntegration_RemoteToLocal(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	src := filepath.Join(remoteRoot, "remote-to-local.bin")
	dst := filepath.Join(localRoot, "remote-to-local.bin")
	require.NoError(t, writeSyntheticFile(src, 4*1024*1024, 9))

	_, err = runFileTransfer(context.Background(), client, logicalRemote(src), logicalLocal(dst), transferOpts{workers: 2, chunkSize: 1024 * 1024})
	require.NoError(t, err)

	srcHash := sha256File(t, src)
	dstHash := sha256File(t, dst)
	require.Equal(t, srcHash, dstHash)
}

func TestIntegration_LargeFile(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	src := filepath.Join(localRoot, "large-100mb.bin")
	dst := filepath.Join(remoteRoot, "large-100mb.bin")
	require.NoError(t, writeSyntheticFile(src, 100*1024*1024, 11))

	_, err = runFileTransfer(context.Background(), client, logicalLocal(src), logicalRemote(dst), transferOpts{workers: 4, chunkSize: 4 * 1024 * 1024})
	require.NoError(t, err)

	srcHash := sha256File(t, src)
	dstHash := sha256File(t, dst)
	require.Equal(t, srcHash, dstHash)
}

func TestIntegration_MultipleFiles(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	srcTree := filepath.Join(localRoot, "tree")
	dstTree := filepath.Join(remoteRoot, "tree-copy")
	require.NoError(t, os.MkdirAll(filepath.Join(srcTree, "dir1", "dir2"), 0o755))
	require.NoError(t, writeSyntheticFile(filepath.Join(srcTree, "a.txt"), 64*1024, 1))
	require.NoError(t, writeSyntheticFile(filepath.Join(srcTree, "dir1", "b.bin"), 512*1024, 2))
	require.NoError(t, writeSyntheticFile(filepath.Join(srcTree, "dir1", "dir2", "c.log"), 128*1024, 3))

	files := make([]string, 0)
	err = filepath.Walk(srcTree, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	require.NoError(t, err)

	for _, srcPath := range files {
		rel, relErr := filepath.Rel(srcTree, srcPath)
		require.NoError(t, relErr)
		dstPath := filepath.Join(dstTree, rel)

		_, runErr := runFileTransfer(context.Background(), client, logicalLocal(srcPath), logicalRemote(dstPath), transferOpts{workers: 2, chunkSize: 256 * 1024})
		require.NoError(t, runErr)
	}

	for _, srcPath := range files {
		rel, relErr := filepath.Rel(srcTree, srcPath)
		require.NoError(t, relErr)
		dstPath := filepath.Join(dstTree, rel)

		srcHash := sha256File(t, srcPath)
		dstHash := sha256File(t, dstPath)
		require.Equal(t, srcHash, dstHash)
	}
}

func TestIntegration_Resume(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()
	checkpointHome := t.TempDir()

	src := filepath.Join(localRoot, "resume-src.bin")
	dst := filepath.Join(remoteRoot, "resume-dst.bin")
	require.NoError(t, writeSyntheticFile(src, 24*1024*1024, 13))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(650*time.Millisecond, cancel)

	checkpointID, err := runFileTransfer(ctx, client, logicalLocal(src), logicalRemote(dst), transferOpts{
		workers:        1,
		limitMbps:      12,
		chunkSize:      1024 * 1024,
		checkpointHome: checkpointHome,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled"))
	require.NotEmpty(t, checkpointID)

	srcHash := sha256File(t, src)
	partialHash := sha256File(t, dst)
	require.NotEqual(t, srcHash, partialHash)

	_, err = runFileTransfer(context.Background(), client, logicalLocal(src), logicalRemote(dst), transferOpts{
		workers:        1,
		limitMbps:      12,
		chunkSize:      1024 * 1024,
		checkpointHome: checkpointHome,
		resumeID:       checkpointID,
	})
	require.NoError(t, err)

	resumedHash := sha256File(t, dst)
	require.Equal(t, srcHash, resumedHash)
}

func TestIntegration_RateLimit(t *testing.T) {
	t.Parallel()

	sshClient, stopServer := startInProcessSSHServer(t)
	defer stopServer()
	defer sshClient.Close()

	remote, err := pkgsftp.NewClient(sshClient)
	require.NoError(t, err)
	defer remote.Close()

	client := &integrationClient{remote: remote}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	src := filepath.Join(localRoot, "rate-src.bin")
	dst := filepath.Join(remoteRoot, "rate-dst.bin")
	fileSize := uint64(4 * 1024 * 1024)
	limitMbps := 8
	require.NoError(t, writeSyntheticFile(src, fileSize, 17))

	start := time.Now()
	_, err = runFileTransfer(context.Background(), client, logicalLocal(src), logicalRemote(dst), transferOpts{
		workers:   1,
		limitMbps: limitMbps,
		chunkSize: 1024 * 1024,
	})
	require.NoError(t, err)
	elapsed := time.Since(start)

	bytesPerSec := float64(limitMbps * 125000)
	expectedSeconds := math.Max(0, (float64(fileSize)-bytesPerSec)/bytesPerSec)
	lower := expectedSeconds * 0.8
	upper := expectedSeconds * 1.2
	actual := elapsed.Seconds()
	require.GreaterOrEqual(t, actual, lower)
	require.LessOrEqual(t, actual, upper)

	srcHash := sha256File(t, src)
	dstHash := sha256File(t, dst)
	require.Equal(t, srcHash, dstHash)
}

func runFileTransfer(ctx context.Context, client transfer.SFTPClient, srcPath, dstPath string, opts transferOpts) (string, error) {
	size, err := logicalFileSize(srcPath)
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
	chunks, err := chunker.Chunk(transfer.FileInfo{ID: "integration-file", Size: size}, chunkCfg)
	if err != nil {
		return "", err
	}
	for i := range chunks {
		chunks[i].ID = fmt.Sprintf("integration-file#%d", i+1)
		chunks[i].FileID = "integration-file"
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
	spec := transfer.FileTransfer{SrcPath: srcPath, DstPath: dstPath, Direction: transfer.L2R}
	logger := logrus.NewEntry(logrus.New())
	factory := integrationWorkerFactory{client: client, logger: logger}

	results := scheduler.Schedule(ctx, chunks, spec, factory)
	var firstErr error
	for result := range results {
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

type integrationWorkerFactory struct {
	client transfer.SFTPClient
	logger *logrus.Entry
}

func (f integrationWorkerFactory) New(_ *ssh.Client, _ *ssh.Client, _ transfer.TransferDirection) (transfer.TransferWorker, error) {
	return transfer.NewDefaultTransferWorker(f.client, f.logger), nil
}

func logicalFileSize(path string) (uint64, error) {
	resolved, _ := parseLogicalPath(path)
	info, err := os.Stat(resolved)
	if err != nil {
		return 0, err
	}
	return uint64(info.Size()), nil
}

func chunkByID(chunks []transfer.Chunk, id string) (transfer.Chunk, bool) {
	for _, ch := range chunks {
		if ch.ID == id {
			return ch, true
		}
	}
	return transfer.Chunk{}, false
}

func parseLogicalPath(path string) (string, bool) {
	if strings.HasPrefix(path, "remote:") {
		return strings.TrimPrefix(path, "remote:"), true
	}
	return strings.TrimPrefix(path, "local:"), false
}

func logicalLocal(path string) string { return "local:" + path }
func logicalRemote(path string) string { return "remote:" + path }

func writeSyntheticFile(path string, size uint64, seed int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rng := mrand.New(mrand.NewSource(seed))
	buf := make([]byte, 1024*1024)
	remaining := size
	for remaining > 0 {
		chunk := uint64(len(buf))
		if remaining < chunk {
			chunk = remaining
		}
		if _, err := rng.Read(buf[:chunk]); err != nil {
			return err
		}
		if _, err := f.Write(buf[:chunk]); err != nil {
			return err
		}
		remaining -= chunk
	}
	return nil
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	h := sha256.New()
	_, err = io.Copy(h, f)
	require.NoError(t, err)
	return hex.EncodeToString(h.Sum(nil))
}

func startInProcessSSHServer(t *testing.T) (*ssh.Client, func()) {
	t.Helper()

	hostKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(hostKey)
	require.NoError(t, err)

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, cfg)
		}
	}()

	clientCfg := &ssh.ClientConfig{
		User:            "integration",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}

	client, err := ssh.Dial("tcp", ln.Addr().String(), clientCfg)
	require.NoError(t, err)

	stop := func() {
		_ = client.Close()
		_ = ln.Close()
		<-done
	}
	return client, stop
}

func handleSSHConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unsupported channel")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			defer channel.Close()
			for req := range in {
				ok := false
				if req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp" {
					ok = true
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					srv, err := pkgsftp.NewServer(channel)
					if err != nil {
						return
					}
					if err := srv.Serve(); err == io.EOF {
						_ = srv.Close()
					}
					return
				}
				if req.WantReply {
					_ = req.Reply(ok, nil)
				}
			}
		}(requests)
	}
}
