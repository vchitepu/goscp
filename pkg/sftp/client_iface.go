package sftp

import (
	"io"
	"os"
)

// SFTPClient abstracts remote SFTP file operations.
type SFTPClient interface {
	Open(path string) (io.ReadCloser, error)
	Create(path string) (io.WriteCloser, error)
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Close() error
}
