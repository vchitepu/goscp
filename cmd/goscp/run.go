package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/vchitepu/goscp/pkg/checkpoint"
	"github.com/vchitepu/goscp/pkg/path"
	"github.com/vchitepu/goscp/pkg/progress"
	"github.com/vchitepu/goscp/pkg/transfer"
	gssh "golang.org/x/crypto/ssh"
)

const (
	exitCodeSuccess = 0
	exitCodePartial = 1
	exitCodeFatal   = 2
)

type runContext struct {
	opts         *cliOptions
	logger       *logrus.Logger
	fs           afero.Fs
	resolver     path.PathResolver
	chunker      transfer.FileChunker
	scheduler    transfer.TransferScheduler
	reporter     progress.ProgressReporter
	checkpointer checkpoint.Checkpointer

	checkpointID string
	checkpointOn bool
	chunkByID    map[string]transfer.Chunk
}

type noopDialerPool struct{}

func (p *noopDialerPool) Acquire(_ context.Context, _ transfer.TransferDirection) (srcConn *gssh.Client, dstConn *gssh.Client, err error) {
	return nil, nil, nil
}

func (p *noopDialerPool) Release(_ *gssh.Client, _ *gssh.Client) {}

type localWorkerFactory struct {
	fs     afero.Fs
	logger *logrus.Logger
}

func (f *localWorkerFactory) New(_ *gssh.Client, _ *gssh.Client, _ transfer.TransferDirection) (transfer.TransferWorker, error) {
	if f.logger == nil {
		return transfer.NewDefaultTransferWorker(&localFileSFTP{fs: f.fs}, logrus.WithField("component", "local_transfer_worker")), nil
	}
	return transfer.NewDefaultTransferWorker(&localFileSFTP{fs: f.fs}, f.logger.WithField("component", "local_transfer_worker")), nil
}

type localFileSFTP struct {
	fs afero.Fs
}

func (s *localFileSFTP) filesystem() afero.Fs {
	if s.fs == nil {
		return afero.NewOsFs()
	}
	return s.fs
}

func (s *localFileSFTP) ReadAt(path string, p []byte, off int64) (int, error) {
	f, err := s.filesystem().Open(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = f.Close()
	}()
	return f.ReadAt(p, off)
}

func (s *localFileSFTP) WriteAt(path string, p []byte, off int64) (int, error) {
	f, err := s.filesystem().OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = f.Close()
	}()
	return f.WriteAt(p, off)
}

func RunTransfer(parent context.Context, logger *logrus.Logger, opts *cliOptions, args []string) (int, error) {
	if opts == nil {
		return exitCodeFatal, errors.New("missing options")
	}
	if logger == nil {
		logger = logrus.New()
		logger.SetOutput(os.Stdout)
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	fs := afero.NewOsFs()
	resolver := path.NewDefaultPathResolver(fs)
	chunker := transfer.NewDefaultFileChunker()
	scheduler := transfer.NewDefaultScheduler(transfer.SchedulerConfig{
		NumWorkers: opts.Connections,
		LimitMbps:  opts.LimitMbps,
	}, &noopDialerPool{})
	reporter := selectReporter(opts, logger)

	run := &runContext{
		opts:      opts,
		logger:    logger,
		fs:        fs,
		resolver:  resolver,
		chunker:   chunker,
		scheduler: scheduler,
		reporter:  reporter,
		chunkByID: make(map[string]transfer.Chunk),
	}

	srcSpecs, dstSpec, srcRaw, dstRaw := parseSpecs(args)
	transfers, err := resolver.Resolve(srcSpecs, dstSpec)
	if err != nil {
		return exitCodeFatal, errors.Wrap(err, "resolve paths")
	}
	if len(transfers) == 0 {
		return exitCodeFatal, errors.New("no files resolved for transfer")
	}

	chunkCfg, err := buildChunkConfig(opts.ChunkSize, opts.Connections)
	if err != nil {
		return exitCodeFatal, err
	}

	chunksByFile := make(map[string][]transfer.Chunk, len(transfers))
	fileInfos := make([]transfer.FileInfo, 0, len(transfers))
	chunkToFile := make(map[string]string)
	allChunks := make([]transfer.Chunk, 0)

	for i, tr := range transfers {
		fileID := makeFileID(i, tr)
		finfo := transfer.FileInfo{ID: fileID, Size: tr.Size}
		fileInfos = append(fileInfos, finfo)

		chunks, chunkErr := run.chunker.Chunk(finfo, chunkCfg)
		if chunkErr != nil {
			return exitCodeFatal, errors.Wrapf(chunkErr, "chunk %s", tr.SrcPath)
		}
		for idx := range chunks {
			chunks[idx].ID = fmt.Sprintf("%s#%d", fileID, idx+1)
			chunks[idx].FileID = fileID
			chunkToFile[chunks[idx].ID] = fileID
			run.chunkByID[chunks[idx].ID] = chunks[idx]
			allChunks = append(allChunks, chunks[idx])
		}
		chunksByFile[fileID] = chunks
	}

	if opts.Resume != "" || opts.Checkpoint {
		if err := run.initCheckpoint(srcRaw, dstRaw, allChunks); err != nil {
			return exitCodeFatal, err
		}
	}

	if opts.DryRun {
		printDryRunPlan(run, srcRaw, dstRaw, transfers, chunksByFile)
		return exitCodeSuccess, nil
	}

	pendingByFile := chunksByFile
	if opts.Resume != "" {
		resumed, resumeErr := run.pendingChunksFromCheckpoint(chunksByFile)
		if resumeErr != nil {
			return exitCodeFatal, resumeErr
		}
		pendingByFile = resumed
	}

	for _, fi := range fileInfos {
		run.reporter.Add(fi)
	}

	var (
		partialFailures bool
		fatalErr        error
	)

	for idx, tr := range transfers {
		fileID := makeFileID(idx, tr)
		chunks := pendingByFile[fileID]
		if len(chunks) == 0 {
			continue
		}

		spec := mapTransferSpec(tr)
		results := run.scheduler.Schedule(ctx, chunks, spec, &localWorkerFactory{fs: run.fs, logger: run.logger})
		for res := range results {
			if res.ChunkID == "" {
				if res.Err != nil {
					fatalErr = res.Err
				}
				continue
			}

			fileResult := res
			if fid, ok := chunkToFile[res.ChunkID]; ok {
				fileResult.ChunkID = fid
			}
			run.reporter.Update(fileResult)

			if res.Err != nil {
				partialFailures = true
				if markErr := run.markChunk(res.ChunkID, transfer.ChunkFailed, 0); markErr != nil && fatalErr == nil {
					fatalErr = errors.Wrapf(markErr, "update failed checkpoint chunk %s", res.ChunkID)
				}
				continue
			}
			if markErr := run.markChunk(res.ChunkID, transfer.ChunkDone, res.BytesTransferred); markErr != nil && fatalErr == nil {
				fatalErr = errors.Wrapf(markErr, "update done checkpoint chunk %s", res.ChunkID)
			}
		}
	}

	run.reporter.Complete()

	if fatalErr != nil {
		return exitCodeFatal, fatalErr
	}
	if partialFailures {
		return exitCodePartial, errors.New("transfer completed with partial failures")
	}
	return exitCodeSuccess, nil
}

func configureLogger(opts *cliOptions, logger *logrus.Logger) (*os.File, error) {
	if logger == nil {
		return nil, nil
	}

	if opts != nil {
		logPath := strings.TrimSpace(opts.LogFile)
		if logPath != "" {
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				return nil, err
			}
			logger.SetOutput(f)
			if opts.Quiet {
				logger.SetLevel(logrus.ErrorLevel)
			} else if opts.Verbose > 1 {
				logger.SetLevel(logrus.DebugLevel)
			} else {
				logger.SetLevel(logrus.InfoLevel)
			}
			return f, nil
		}
	}

	logOutput := ""
	if opts != nil {
		logOutput = opts.LogOutput
	}
	switch strings.ToLower(strings.TrimSpace(logOutput)) {
	case "", "stdout":
		logger.SetOutput(os.Stdout)
	case "stderr":
		logger.SetOutput(os.Stderr)
	default:
		logger.SetOutput(os.Stdout)
	}

	if opts != nil && opts.Quiet {
		logger.SetLevel(logrus.ErrorLevel)
	} else if opts != nil && opts.Verbose > 1 {
		logger.SetLevel(logrus.DebugLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	return nil, nil
}

func selectReporter(opts *cliOptions, logger *logrus.Logger) progress.ProgressReporter {
	entry := logrus.NewEntry(logger)
	if opts.Quiet {
		return progress.NewQuietReporter(entry)
	}
	return progress.NewLogReporter(entry)
}

func parseSpecs(args []string) (srcSpecs []path.PathSpec, dstSpec path.PathSpec, srcRaw []string, dstRaw string) {
	srcRaw = append([]string(nil), args[:len(args)-1]...)
	dstRaw = args[len(args)-1]
	srcSpecs = make([]path.PathSpec, 0, len(srcRaw))
	for _, src := range srcRaw {
		srcSpecs = append(srcSpecs, path.ParsePathSpec(src))
	}
	dstSpec = path.ParsePathSpec(dstRaw)
	return srcSpecs, dstSpec, srcRaw, dstRaw
}

func buildChunkConfig(raw string, workers int) (transfer.ChunkConfig, error) {
	cfg := transfer.ChunkConfig{NumWorkers: workers}
	if workers <= 0 {
		cfg.NumWorkers = 1
	}
	if raw == "" || strings.EqualFold(raw, "auto") {
		return cfg, nil
	}
	v, err := parseByteSize(raw)
	if err != nil {
		return transfer.ChunkConfig{}, errors.Wrapf(err, "invalid chunk-size %q", raw)
	}
	cfg.MinSize = v
	cfg.MaxSize = v
	return cfg, nil
}

func parseByteSize(raw string) (uint64, error) {
	s := strings.TrimSpace(strings.ToUpper(raw))
	s = strings.TrimSuffix(s, "B")
	if s == "" {
		return 0, errors.New("empty value")
	}
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "K"):
		mult = 1024
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "G"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "T"):
		mult = 1024 * 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "T")
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, errors.New("must be > 0")
	}
	return n * mult, nil
}

func mapTransferSpec(in path.FileTransfer) transfer.FileTransfer {
	return transfer.FileTransfer{
		SrcPath:   in.SrcPath,
		DstPath:   in.DstPath,
		SrcHost:   in.SrcHost,
		DstHost:   in.DstHost,
		Direction: mapDirection(in.Direction),
	}
}

func mapDirection(d path.TransferDirection) transfer.TransferDirection {
	switch d {
	case path.R2L:
		return transfer.R2L
	case path.R2R:
		return transfer.R2R
	default:
		return transfer.L2R
	}
}

func makeFileID(i int, tr path.FileTransfer) string {
	return fmt.Sprintf("f%04d:%s->%s", i+1, tr.SrcPath, tr.DstPath)
}

func printDryRunPlan(run *runContext, srcRaw []string, dstRaw string, transfers []path.FileTransfer, chunksByFile map[string][]transfer.Chunk) {
	type row struct {
		FileID    string
		Direction string
		Src       string
		Dst       string
		Size      uint64
		Chunks    int
	}
	rows := make([]row, 0, len(transfers))
	for i, tr := range transfers {
		fid := makeFileID(i, tr)
		rows = append(rows, row{
			FileID:    fid,
			Direction: string(tr.Direction),
			Src:       tr.SrcPath,
			Dst:       tr.DstPath,
			Size:      tr.Size,
			Chunks:    len(chunksByFile[fid]),
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].FileID < rows[j].FileID })

	fmt.Println("GoSCP dry-run transfer plan")
	fmt.Printf("version=%s\n", version)
	fmt.Printf("sources=%s\n", strings.Join(srcRaw, ", "))
	fmt.Printf("destination=%s\n", dstRaw)
	fmt.Printf("connections=%d sftp_sessions=%d chunk_size=%s limit_mbps=%d\n", run.opts.Connections, run.opts.SFTPSessions, run.opts.ChunkSize, run.opts.LimitMbps)
	fmt.Printf("identity=%q ssh_config=%q login=%q port=%d ssh_options=%d ipv4=%t ipv6=%t compress=%t checkpoint=%t resume=%q\n",
		run.opts.Identity,
		run.opts.SSHConfig,
		run.opts.Login,
		run.opts.Port,
		len(run.opts.SSHOptions),
		run.opts.IPv4,
		run.opts.IPv6,
		run.opts.Compress,
		run.opts.Checkpoint,
		run.opts.Resume,
	)
	fmt.Printf("files=%d\n", len(rows))
	for _, r := range rows {
		fmt.Printf("- %s dir=%s size=%d chunks=%d src=%s dst=%s\n", r.FileID, r.Direction, r.Size, r.Chunks, r.Src, r.Dst)
	}
}

func (r *runContext) initCheckpoint(srcRaw []string, dstRaw string, allChunks []transfer.Chunk) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "resolve home dir for checkpoint")
	}
	r.checkpointer = checkpoint.NewFileCheckpointer(r.fs, home)
	r.checkpointOn = true

	if r.opts.Resume != "" {
		r.checkpointID = r.opts.Resume
		_, loadErr := r.checkpointer.Load(r.checkpointID)
		if loadErr != nil {
			return errors.Wrapf(loadErr, "load checkpoint %s", r.checkpointID)
		}
		return nil
	}

	created := time.Now().UTC()
	state := checkpoint.CheckpointState{
		ID:        checkpoint.ComputeCheckpointID(checkpoint.TransferSpec{SrcPaths: srcRaw, DstPath: dstRaw}, created),
		CreatedAt: created,
		Spec: checkpoint.TransferSpec{
			SrcPaths: srcRaw,
			DstPath:  dstRaw,
		},
		Chunks: append([]transfer.Chunk(nil), allChunks...),
	}
	if err := r.checkpointer.Save(state); err != nil {
		return errors.Wrap(err, "save checkpoint")
	}
	r.checkpointID = state.ID
	return nil
}

func (r *runContext) pendingChunksFromCheckpoint(planned map[string][]transfer.Chunk) (map[string][]transfer.Chunk, error) {
	if !r.checkpointOn || r.checkpointID == "" {
		return planned, nil
	}
	state, err := r.checkpointer.Load(r.checkpointID)
	if err != nil {
		return nil, errors.Wrapf(err, "load checkpoint %s", r.checkpointID)
	}

	byID := make(map[string]transfer.Chunk, len(state.Chunks))
	for _, ch := range state.Chunks {
		byID[ch.ID] = ch
		r.chunkByID[ch.ID] = ch
	}

	pending := make(map[string][]transfer.Chunk, len(planned))
	for fileID, chunks := range planned {
		list := make([]transfer.Chunk, 0, len(chunks))
		for _, ch := range chunks {
			if checkpointChunk, ok := byID[ch.ID]; ok {
				if checkpointChunk.State == transfer.ChunkDone {
					continue
				}
				ch = checkpointChunk
			}
			list = append(list, ch)
		}
		pending[fileID] = list
	}
	return pending, nil
}

func (r *runContext) markChunk(chunkID string, state transfer.ChunkState, done uint64) error {
	if chunkID == "" {
		return nil
	}
	ch, ok := r.chunkByID[chunkID]
	if !ok {
		return nil
	}
	ch.State = state
	if done > 0 {
		ch.Done = done
	}
	r.chunkByID[chunkID] = ch

	if !r.checkpointOn || r.checkpointer == nil || r.checkpointID == "" {
		return nil
	}
	return r.checkpointer.UpdateChunk(r.checkpointID, ch)
}

var _ io.ReaderAt = (*os.File)(nil)
var _ io.WriterAt = (*os.File)(nil)
