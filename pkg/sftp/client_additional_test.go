package sftp

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDefaultSFTPClient_DefaultsToOsFs(t *testing.T) {
	t.Parallel()

	var raw *pkgsftp.Client
	client := NewDefaultSFTPClient(raw, nil)
	require.NotNil(t, client)
	require.NotNil(t, client.fs)
	_, ok := client.fs.(*afero.OsFs)
	assert.True(t, ok)
}

func TestMkdirAllAndClose_Branches(t *testing.T) {
	t.Parallel()

	remote := &mockRemoteClient{}
	remote.On("MkdirAll", "/remote/newdir").Return(errors.New("mkdir failed")).Once()
	remote.On("Close").Return(errors.New("close failed")).Once()

	client := NewDefaultSFTPClientWithRemote(remote, afero.NewMemMapFs())
	err := client.MkdirAll("/remote/newdir", 0o755)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir failed")

	err = client.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "close failed")
	remote.AssertExpectations(t)
}

func TestClose_NilRemote(t *testing.T) {
	t.Parallel()

	client := &DefaultSFTPClient{fs: afero.NewMemMapFs(), logger: logrus.WithField("component", "test")}
	require.NoError(t, client.Close())
}

func TestMockSFTPClient_Methods(t *testing.T) {
	t.Parallel()

	m := &MockSFTPClient{}
	m.On("Open", "/in").Return(io.NopCloser(&readerStub{}), nil).Once()
	m.On("Create", "/out").Return(&writeCloserStub{}, nil).Once()
	m.On("Stat", "/stat").Return(testFileInfo{name: "x"}, nil).Once()
	m.On("ReadDir", "/dir").Return([]os.FileInfo{testFileInfo{name: "x"}}, nil).Once()
	m.On("MkdirAll", "/mkdir", os.FileMode(0o755)).Return(nil).Once()
	m.On("Close").Return(nil).Once()

	rc, err := m.Open("/in")
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	wc, err := m.Create("/out")
	require.NoError(t, err)
	_, err = wc.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, wc.Close())

	fi, err := m.Stat("/stat")
	require.NoError(t, err)
	assert.Equal(t, "x", fi.Name())

	list, err := m.ReadDir("/dir")
	require.NoError(t, err)
	assert.Len(t, list, 1)

	require.NoError(t, m.MkdirAll("/mkdir", 0o755))
	require.NoError(t, m.Close())
	m.AssertExpectations(t)
}

type readerStub struct{}

func (r *readerStub) Read(p []byte) (int, error) {
	return 0, io.EOF
}

type testFileInfo struct{ name string }

func (t testFileInfo) Name() string       { return t.name }
func (t testFileInfo) Size() int64        { return 0 }
func (t testFileInfo) Mode() os.FileMode  { return 0 }
func (t testFileInfo) ModTime() time.Time { return time.Time{} }
func (t testFileInfo) IsDir() bool        { return false }
func (t testFileInfo) Sys() any           { return nil }

type writeCloserStub struct{}

func (w *writeCloserStub) Write(p []byte) (int, error) { return len(p), nil }
func (w *writeCloserStub) Close() error                { return nil }
