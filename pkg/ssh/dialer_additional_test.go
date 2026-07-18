package ssh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	gssh "golang.org/x/crypto/ssh"
)

func TestBuildClientConfig_ValidationErrors(t *testing.T) {
	t.Parallel()

	d := NewDefaultSSHDialer()

	_, err := d.buildClientConfig(DialOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ssh user is required")

	_, err = d.buildClientConfig(DialOptions{User: "vinay"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "known_hosts path is required")

	tempDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tempDir)
	_, err = d.buildClientConfig(DialOptions{User: "vinay", PrivateKeyPath: keyPath, KnownHostsPath: "/does/not/exist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create known_hosts callback")
}

func TestBuildAuthMethods_InvalidPrivateKey(t *testing.T) {
	t.Parallel()

	d := NewDefaultSSHDialer()
	_, err := d.buildAuthMethods(DialOptions{PrivateKeyPath: "/missing/key"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load private key")
}

func TestLoadPrivateKeySigner_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := loadPrivateKeySigner("missing-private-key")
	require.Error(t, err)
}

func TestBuildSSHAgentAuth_NoSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	_, err := buildSSHAgentAuth()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH_AUTH_SOCK not set")
}

func TestDefaultProxyDial_Guards(t *testing.T) {
	t.Parallel()

	d := NewDefaultSSHDialer()
	_, err := d.defaultProxyDial(context.Background(), nil, "target:22", &gssh.ClientConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jump client is nil")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.defaultProxyDial(ctx, &gssh.Client{}, "target:22", &gssh.ClientConfig{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

func TestDefaultDirectDial_DialError(t *testing.T) {
	t.Parallel()

	d := NewDefaultSSHDialer()
	_, err := d.defaultDirectDial(context.Background(), "127.0.0.1:1", &gssh.ClientConfig{Timeout: 20 * time.Millisecond})
	require.Error(t, err)
}

func TestClose_EmptyAndTrackNil(t *testing.T) {
	t.Parallel()

	d := NewDefaultSSHDialer()
	d.trackClient(nil)
	require.NoError(t, d.Close())
}

func TestMockSSHDialer_Methods(t *testing.T) {
	t.Parallel()

	m := &MockSSHDialer{}
	m.On("Dial", mock.Anything, "host", mock.Anything).Return((*gssh.Client)(nil), errors.New("dial fail")).Once()
	m.On("Close").Return(nil).Once()

	_, err := m.Dial(context.Background(), "host", DialOptions{User: "vinay"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial fail")
	require.NoError(t, m.Close())
	m.AssertExpectations(t)
}
