package sftp

import (
	"io"
	"os"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockRemoteClient struct {
	mock.Mock
}

func (m *mockRemoteClient) Stat(path string) (os.FileInfo, error) {
	args := m.Called(path)
	fi, _ := args.Get(0).(os.FileInfo)
	return fi, args.Error(1)
}

func (m *mockRemoteClient) ReadDir(path string) ([]os.FileInfo, error) {
	args := m.Called(path)
	files, _ := args.Get(0).([]os.FileInfo)
	return files, args.Error(1)
}

func (m *mockRemoteClient) Create(path string) (io.WriteCloser, error) {
	args := m.Called(path)
	wc, _ := args.Get(0).(io.WriteCloser)
	return wc, args.Error(1)
}

func (m *mockRemoteClient) Open(path string) (io.ReadCloser, error) {
	args := m.Called(path)
	rc, _ := args.Get(0).(io.ReadCloser)
	return rc, args.Error(1)
}

func (m *mockRemoteClient) MkdirAll(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

func (m *mockRemoteClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestStat_Remote(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/tmp/sample.txt", []byte("x"), 0o644))
	fi, err := fs.Stat("/tmp/sample.txt")
	require.NoError(t, err)

	remote := &mockRemoteClient{}
	remote.On("Stat", "/remote/sample.txt").Return(fi, nil).Once()

	client := NewDefaultSFTPClientWithRemote(remote, fs)
	got, err := client.Stat("/remote/sample.txt")
	require.NoError(t, err)
	assert.Equal(t, fi.Name(), got.Name())
	remote.AssertExpectations(t)
}

func TestReadDir_Remote(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/tmp/dir", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/tmp/dir/a.txt", []byte("a"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/tmp/dir/b.txt", []byte("b"), 0o644))
	files, err := afero.ReadDir(fs, "/tmp/dir")
	require.NoError(t, err)

	remote := &mockRemoteClient{}
	remote.On("ReadDir", "/remote/dir").Return(files, nil).Once()

	client := NewDefaultSFTPClientWithRemote(remote, fs)
	got, err := client.ReadDir("/remote/dir")
	require.NoError(t, err)
	assert.Len(t, got, 2)
	remote.AssertExpectations(t)
}

func TestCreate_Remote(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writer, err := fs.Create("/tmp/out.txt")
	require.NoError(t, err)

	remote := &mockRemoteClient{}
	remote.On("Create", "/remote/out.txt").Return(writer, nil).Once()

	client := NewDefaultSFTPClientWithRemote(remote, fs)
	wc, err := client.Create("/remote/out.txt")
	require.NoError(t, err)
	_, err = wc.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, wc.Close())
	remote.AssertExpectations(t)
}

func TestOpen_Remote(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/tmp/in.txt", []byte("payload"), 0o644))
	reader, err := fs.Open("/tmp/in.txt")
	require.NoError(t, err)

	remote := &mockRemoteClient{}
	remote.On("Open", "/remote/in.txt").Return(reader, nil).Once()

	client := NewDefaultSFTPClientWithRemote(remote, fs)
	rc, err := client.Open("/remote/in.txt")
	require.NoError(t, err)
	content, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, "payload", string(content))
	remote.AssertExpectations(t)
}
