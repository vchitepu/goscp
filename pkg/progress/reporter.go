package progress

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

type fileProgress struct {
	info        FileInfo
	bar         *mpb.Bar
	transferred uint64
	done        bool
	failed      bool
	err         error
}

// MultiBarReporter renders one bar per file and one overall bar.
type MultiBarReporter struct {
	mu               sync.Mutex
	logger           *logrus.Entry
	progress         *mpb.Progress
	files            map[string]*fileProgress
	overallBar       *mpb.Bar
	totalBytes       uint64
	transferredBytes uint64
	completed        bool
}

// NewMultiBarReporter creates a MultiBarReporter that writes bars to stderr.
func NewMultiBarReporter(logger *logrus.Entry) *MultiBarReporter {
	return NewMultiBarReporterWithOutput(logger, io.Writer(nil))
}

// NewMultiBarReporterWithOutput creates a MultiBarReporter with a custom bar output.
func NewMultiBarReporterWithOutput(logger *logrus.Entry, output io.Writer) *MultiBarReporter {
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}
	opts := []mpb.ContainerOption{}
	if output != nil {
		opts = append(opts, mpb.WithOutput(output))
	}
	p := mpb.New(opts...)

	r := &MultiBarReporter{
		logger:   logger,
		progress: p,
		files:    make(map[string]*fileProgress),
	}
	r.overallBar = p.AddBar(0,
		mpb.PrependDecorators(
			decor.Name("overall", decor.WCSyncSpaceR),
			decor.CountersKibiByte(" % .2f / % .2f"),
		),
		mpb.AppendDecorators(
			decor.EwmaSpeed(decor.SizeB1024(0), " % .2f MB/s", 60),
			decor.OnComplete(decor.EwmaETA(decor.ET_STYLE_GO, 60), " done"),
		),
	)
	return r
}

func fileDisplayName(f FileInfo) string {
	if f.ID == "" {
		return "file"
	}
	return f.ID
}

func (r *MultiBarReporter) Add(f FileInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.completed {
		return
	}
	if _, exists := r.files[f.ID]; exists {
		return
	}

	name := fileDisplayName(f)
	bar := r.progress.AddBar(int64(f.Size),
		mpb.PrependDecorators(
			decor.Name(name, decor.WCSyncSpaceR),
			decor.CountersKibiByte(" % .2f / % .2f"),
		),
		mpb.AppendDecorators(
			decor.EwmaSpeed(decor.SizeB1024(0), " % .2f MB/s", 60),
			decor.OnComplete(decor.EwmaETA(decor.ET_STYLE_GO, 60), " done"),
		),
	)

	r.files[f.ID] = &fileProgress{info: f, bar: bar}
	r.totalBytes += f.Size
	r.overallBar.SetTotal(int64(r.totalBytes), false)
}

func (r *MultiBarReporter) Update(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.completed {
		return
	}
	fp, ok := r.files[res.ChunkID]
	if !ok {
		return
	}

	fp.transferred += res.BytesTransferred
	r.transferredBytes += res.BytesTransferred

	if fp.bar != nil {
		fp.bar.IncrInt64(int64(res.BytesTransferred))
	}
	r.overallBar.IncrInt64(int64(res.BytesTransferred))

	if res.Err != nil {
		fp.failed = true
		fp.done = true
		fp.err = res.Err
		if fp.bar != nil {
			fp.bar.Abort(false)
		}
	}
}

func (r *MultiBarReporter) Complete() {
	r.mu.Lock()
	if r.completed {
		r.mu.Unlock()
		return
	}
	r.completed = true
	for _, fp := range r.files {
		if fp.done {
			continue
		}
		fp.done = true
		if fp.bar != nil {
			fp.bar.SetCurrent(int64(fp.info.Size))
			fp.bar.SetTotal(int64(fp.info.Size), true)
		}
	}
	r.overallBar.SetCurrent(int64(r.transferredBytes))
	r.overallBar.SetTotal(int64(r.totalBytes), true)
	r.mu.Unlock()

	r.progress.Wait()
}

func (r *MultiBarReporter) Fail(f FileInfo, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fp, ok := r.files[f.ID]
	if !ok {
		return
	}
	fp.failed = true
	fp.done = true
	fp.err = err
	if fp.bar != nil {
		fp.bar.Abort(false)
	}
	r.logger.WithFields(logrus.Fields{
		"file_id": f.ID,
		"size":    f.Size,
		"error":   err,
	}).Error("file transfer failed")
}

// QuietReporter logs progress lifecycle without rendering terminal bars.
type QuietReporter struct {
	logger *logrus.Entry
}

func NewQuietReporter(logger *logrus.Entry) *QuietReporter {
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}
	return &QuietReporter{logger: logger}
}

func (r *QuietReporter) Add(f FileInfo) {
	r.logger.WithFields(logrus.Fields{"file_id": f.ID, "size": f.Size}).Info("quiet reporter add")
}

func (r *QuietReporter) Update(res Result) {
	entry := r.logger.WithFields(logrus.Fields{
		"chunk_id":           res.ChunkID,
		"bytes_transferred":  res.BytesTransferred,
	})
	if res.Err != nil {
		entry = entry.WithField("error", res.Err)
	}
	entry.Info("quiet reporter update")
}

func (r *QuietReporter) Complete() {
	r.logger.Info("quiet reporter complete")
}

func (r *QuietReporter) Fail(f FileInfo, err error) {
	r.logger.WithFields(logrus.Fields{"file_id": f.ID, "error": err}).Info("quiet reporter fail")
}

// LogReporter writes structured per-event logs.
type LogReporter struct {
	mu               sync.Mutex
	logger           *logrus.Entry
	files            map[string]FileInfo
	transferred      map[string]uint64
	totalBytes       uint64
	transferredBytes uint64
}

func NewLogReporter(logger *logrus.Entry) *LogReporter {
	if logger == nil {
		logger = logrus.NewEntry(logrus.New())
	}
	return &LogReporter{
		logger:      logger,
		files:       make(map[string]FileInfo),
		transferred: make(map[string]uint64),
	}
}

func (r *LogReporter) Add(f FileInfo) {
	r.mu.Lock()
	if _, exists := r.files[f.ID]; exists {
		r.mu.Unlock()
		return
	}
	r.files[f.ID] = f
	r.totalBytes += f.Size
	r.mu.Unlock()

	r.logger.WithFields(logrus.Fields{
		"event":            "file_start",
		"current_file":     f.ID,
		"file_size":        f.Size,
		"percent_complete": 0.0,
	}).Info("progress")
}

func (r *LogReporter) Update(res Result) {
	r.mu.Lock()
	fileInfo, ok := r.files[res.ChunkID]
	if !ok {
		r.mu.Unlock()
		return
	}

	r.transferred[res.ChunkID] += res.BytesTransferred
	r.transferredBytes += res.BytesTransferred

	fileTransferred := r.transferred[res.ChunkID]
	filePercent := 100.0
	if fileInfo.Size > 0 {
		if fileTransferred > fileInfo.Size {
			fileTransferred = fileInfo.Size
		}
		filePercent = float64(fileTransferred) * 100 / float64(fileInfo.Size)
	}

	overallPercent := 100.0
	if r.totalBytes > 0 {
		overallTransferred := r.transferredBytes
		if overallTransferred > r.totalBytes {
			overallTransferred = r.totalBytes
		}
		overallPercent = float64(overallTransferred) * 100 / float64(r.totalBytes)
	}
	r.mu.Unlock()

	fields := logrus.Fields{
		"event":              "file_progress",
		"current_file":       res.ChunkID,
		"bytes_transferred":  res.BytesTransferred,
		"percent_complete":   filePercent,
		"overall_percent":    overallPercent,
	}
	if res.Err != nil {
		fields["error"] = res.Err.Error()
	}
	r.logger.WithFields(fields).Info("progress")
}

func (r *LogReporter) Complete() {
	r.logger.WithFields(logrus.Fields{
		"event":            "complete",
		"percent_complete": 100.0,
	}).Info("progress")
}

func (r *LogReporter) Fail(f FileInfo, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	r.logger.WithFields(logrus.Fields{
		"event":        "fail",
		"current_file": f.ID,
		"error":        msg,
	}).Info("progress")
}

func (r *MultiBarReporter) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprintf("files=%d transferred=%d total=%d completed=%v", len(r.files), r.transferredBytes, r.totalBytes, r.completed)
}

var _ ProgressReporter = (*MultiBarReporter)(nil)
var _ ProgressReporter = (*QuietReporter)(nil)
var _ ProgressReporter = (*LogReporter)(nil)

var _ = time.Second
