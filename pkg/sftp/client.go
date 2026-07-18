package sftp

import (
	"io"
	"os"

	pkgsftp "github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
)

type remoteClient interface {
	Stat(path string) (os.FileInfo, error)
	ReadDir(path string) ([]os.FileInfo, error)
	Create(path string) (io.WriteCloser, error)
	Open(path string) (io.ReadCloser, error)
	MkdirAll(path string) error
	Close() error
}

type sftpRemoteAdapter struct {
	client *pkgsftp.Client
}

func (a *sftpRemoteAdapter) Stat(path string) (os.FileInfo, error) {
	return a.client.Stat(path)
}

func (a *sftpRemoteAdapter) ReadDir(path string) ([]os.FileInfo, error) {
	return a.client.ReadDir(path)
}

func (a *sftpRemoteAdapter) Create(path string) (io.WriteCloser, error) {
	return a.client.Create(path)
}

func (a *sftpRemoteAdapter) Open(path string) (io.ReadCloser, error) {
	return a.client.Open(path)
}

func (a *sftpRemoteAdapter) MkdirAll(path string) error {
	return a.client.MkdirAll(path)
}

func (a *sftpRemoteAdapter) Close() error {
	return a.client.Close()
}

// DefaultSFTPClient wraps github.com/pkg/sftp with an injectable local fs.
type DefaultSFTPClient struct {
	remote remoteClient
	fs     afero.Fs
	logger *logrus.Entry
}

// NewDefaultSFTPClient creates a DefaultSFTPClient from a real *sftp.Client.
func NewDefaultSFTPClient(client *pkgsftp.Client, fs afero.Fs) *DefaultSFTPClient {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return &DefaultSFTPClient{
		remote: &sftpRemoteAdapter{client: client},
		fs:     fs,
		logger: logrus.WithField("component", "sftp_client"),
	}
}

// NewDefaultSFTPClientWithRemote is test-focused constructor with remote stub/mock.
func NewDefaultSFTPClientWithRemote(remote remoteClient, fs afero.Fs) *DefaultSFTPClient {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return &DefaultSFTPClient{
		remote: remote,
		fs:     fs,
		logger: logrus.WithField("component", "sftp_client"),
	}
}

func (c *DefaultSFTPClient) Open(path string) (io.ReadCloser, error) {
	c.logger.WithField("path", path).Debug("sftp open")
	return c.remote.Open(path)
}

func (c *DefaultSFTPClient) Create(path string) (io.WriteCloser, error) {
	c.logger.WithField("path", path).Debug("sftp create")
	return c.remote.Create(path)
}

func (c *DefaultSFTPClient) Stat(path string) (os.FileInfo, error) {
	c.logger.WithField("path", path).Debug("sftp stat")
	return c.remote.Stat(path)
}

func (c *DefaultSFTPClient) ReadDir(path string) ([]os.FileInfo, error) {
	c.logger.WithField("path", path).Debug("sftp readdir")
	return c.remote.ReadDir(path)
}

func (c *DefaultSFTPClient) MkdirAll(path string, _ os.FileMode) error {
	c.logger.WithField("path", path).Debug("sftp mkdirall")
	return c.remote.MkdirAll(path)
}

func (c *DefaultSFTPClient) Close() error {
	c.logger.WithField("op", "close").Debug("sftp close")
	if c.remote == nil {
		return nil
	}
	return c.remote.Close()
}
