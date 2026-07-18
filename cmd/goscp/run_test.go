package main

import (
	"errors"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureLogger_LogOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		logOutput string
		wantOut   *os.File
	}{
		{name: "default_stdout", logOutput: "", wantOut: os.Stdout},
		{name: "explicit_stdout", logOutput: "stdout", wantOut: os.Stdout},
		{name: "explicit_stderr", logOutput: "stderr", wantOut: os.Stderr},
		{name: "invalid_defaults_stdout", logOutput: "something-else", wantOut: os.Stdout},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := logrus.New()
			f, err := configureLogger(&cliOptions{LogOutput: tc.logOutput}, logger)
			require.NoError(t, err)
			require.Nil(t, f)

			assert.Same(t, tc.wantOut, logger.Out)
		})
	}
}

func TestConfigureLogger_LogFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := dir + "/goscp.log"

	logger := logrus.New()
	f, err := configureLogger(&cliOptions{LogOutput: "stderr", LogFile: logPath}, logger)
	require.NoError(t, err)
	require.NotNil(t, f)
	t.Cleanup(func() {
		_ = f.Close()
	})

	logger.WithField("check", "log-file").Info("message")

	data, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "message")
}

func TestParseByteSize_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    uint64
		wantErr string
	}{
		{name: "bytes", input: "512", want: 512},
		{name: "kilobytes", input: "2K", want: 2 * 1024},
		{name: "megabytes_with_b_suffix", input: "3MB", want: 3 * 1024 * 1024},
		{name: "gigabytes", input: "1g", want: 1024 * 1024 * 1024},
		{name: "trimmed_spaces", input: " 4m ", want: 4 * 1024 * 1024},
		{name: "empty", input: "", wantErr: "empty value"},
		{name: "zero", input: "0", wantErr: "must be > 0"},
		{name: "bad_number", input: "x12", wantErr: "invalid syntax"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseByteSize(tc.input)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBuildChunkConfig_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		workers    int
		wantWorker int
		wantMin    uint64
		wantMax    uint64
		wantErr    string
	}{
		{name: "auto_keeps_worker_count", raw: "auto", workers: 4, wantWorker: 4},
		{name: "empty_defaults_auto", raw: "", workers: 2, wantWorker: 2},
		{name: "non_positive_worker_defaults_to_one", raw: "auto", workers: 0, wantWorker: 1},
		{name: "fixed_size", raw: "8M", workers: 3, wantWorker: 3, wantMin: 8 * 1024 * 1024, wantMax: 8 * 1024 * 1024},
		{name: "invalid_size", raw: "not-a-size", workers: 1, wantErr: "invalid chunk-size"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := buildChunkConfig(tc.raw, tc.workers)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantWorker, cfg.NumWorkers)
			assert.Equal(t, tc.wantMin, cfg.MinSize)
			assert.Equal(t, tc.wantMax, cfg.MaxSize)
		})
	}
}

func TestLocalFileSFTP_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		seedPath    string
		seedContent []byte
		readPath    string
		readOffset  int64
		readLen     int
		writePath   string
		writeData   []byte
		writeOffset int64
		wantRead    []byte
		wantReadErr error
		wantWriteErr error
		wantFileAfter string
	}{
		{
			name:         "read_existing_file",
			seedPath:     "/tmp/src.bin",
			seedContent:  []byte("abcdefgh"),
			readPath:     "/tmp/src.bin",
			readOffset:   2,
			readLen:      3,
			wantRead:     []byte("cde"),
			writePath:    "/tmp/dst.bin",
			writeData:    []byte("zz"),
			writeOffset:  0,
			wantFileAfter: "zz",
		},
		{
			name:        "read_missing_file",
			readPath:    "/tmp/missing.bin",
			readOffset:  0,
			readLen:     4,
			wantReadErr: afero.ErrFileNotFound,
			writePath:   "/tmp/dst.bin",
			writeData:   []byte("ok"),
			wantFileAfter: "ok",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := afero.NewMemMapFs()
			if tc.seedPath != "" {
				require.NoError(t, fs.MkdirAll("/tmp", 0o755))
				require.NoError(t, afero.WriteFile(fs, tc.seedPath, tc.seedContent, 0o644))
			}

			sftp := &localFileSFTP{fs: fs}

			buf := make([]byte, tc.readLen)
			n, readErr := sftp.ReadAt(tc.readPath, buf, tc.readOffset)
			if tc.wantReadErr != nil {
				require.Error(t, readErr)
				assert.True(t, errors.Is(readErr, tc.wantReadErr))
			} else {
				require.NoError(t, readErr)
				assert.Equal(t, tc.wantRead, buf[:n])
			}

			_, writeErr := sftp.WriteAt(tc.writePath, tc.writeData, tc.writeOffset)
			if tc.wantWriteErr != nil {
				require.Error(t, writeErr)
				assert.True(t, errors.Is(writeErr, tc.wantWriteErr))
				return
			}
			require.NoError(t, writeErr)

			got, err := afero.ReadFile(fs, tc.writePath)
			require.NoError(t, err)
			assert.Equal(t, tc.wantFileAfter, string(got))
		})
	}
}
