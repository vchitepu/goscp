package sftp

import (
	"io"
	"os"

	"github.com/stretchr/testify/mock"
)

// MockSFTPClient is a testify/mock implementation of SFTPClient.
type MockSFTPClient struct {
	mock.Mock
}

func (m *MockSFTPClient) Open(path string) (io.ReadCloser, error) {
	args := m.Called(path)
	rc, _ := args.Get(0).(io.ReadCloser)
	return rc, args.Error(1)
}

func (m *MockSFTPClient) Create(path string) (io.WriteCloser, error) {
	args := m.Called(path)
	wc, _ := args.Get(0).(io.WriteCloser)
	return wc, args.Error(1)
}

func (m *MockSFTPClient) Stat(path string) (os.FileInfo, error) {
	args := m.Called(path)
	fi, _ := args.Get(0).(os.FileInfo)
	return fi, args.Error(1)
}

func (m *MockSFTPClient) ReadDir(path string) ([]os.FileInfo, error) {
	args := m.Called(path)
	files, _ := args.Get(0).([]os.FileInfo)
	return files, args.Error(1)
}

func (m *MockSFTPClient) MkdirAll(path string, perm os.FileMode) error {
	args := m.Called(path, perm)
	return args.Error(0)
}

func (m *MockSFTPClient) Close() error {
	args := m.Called()
	return args.Error(0)
}
