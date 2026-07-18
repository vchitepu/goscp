package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestDial_Success(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tempDir)
	knownHostsPath := writeKnownHosts(t, tempDir, "target.example.com", generatePublicKey(t))

	dialer := NewDefaultSSHDialer()
	dialer.directDial = func(ctx context.Context, address string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		assert.Equal(t, "target.example.com:22", address)
		assert.NotNil(t, cfg)
		assert.NotNil(t, cfg.HostKeyCallback)
		assert.NotEmpty(t, cfg.Auth)
		return nil, nil
	}

	client, err := dialer.Dial(context.Background(), "target.example.com", DialOptions{
		User:           "vinay",
		Port:           22,
		PrivateKeyPath: keyPath,
		KnownHostsPath: knownHostsPath,
		Timeout:        time.Second,
	})
	require.NoError(t, err)
	assert.Nil(t, client)
}

func TestDial_AuthFailure(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	knownHostsPath := writeKnownHosts(t, tempDir, "target.example.com", generatePublicKey(t))

	dialer := NewDefaultSSHDialer()
	_, err := dialer.Dial(context.Background(), "target.example.com", DialOptions{
		User:           "vinay",
		Port:           22,
		KnownHostsPath: knownHostsPath,
		Timeout:        time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no auth methods")
}

func TestDial_Timeout(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tempDir)
	knownHostsPath := writeKnownHosts(t, tempDir, "target.example.com", generatePublicKey(t))

	dialer := NewDefaultSSHDialer()
	dialer.directDial = func(ctx context.Context, _ string, _ *ssh.ClientConfig) (*ssh.Client, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := dialer.Dial(ctx, "target.example.com", DialOptions{
		User:           "vinay",
		Port:           22,
		PrivateKeyPath: keyPath,
		KnownHostsPath: knownHostsPath,
		Timeout:        10 * time.Second,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
}

func TestDial_ProxyJump(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tempDir)
	knownHostsPath := writeKnownHosts(t, tempDir, "target.example.com", generatePublicKey(t))

	dialer := NewDefaultSSHDialer()
	calls := make([]string, 0, 3)
	dialer.directDial = func(_ context.Context, address string, _ *ssh.ClientConfig) (*ssh.Client, error) {
		calls = append(calls, "direct:"+address)
		return nil, nil
	}
	dialer.proxyDial = func(_ context.Context, _ *ssh.Client, address string, _ *ssh.ClientConfig) (*ssh.Client, error) {
		calls = append(calls, "proxy:"+address)
		return nil, nil
	}

	_, err := dialer.Dial(context.Background(), "target.example.com", DialOptions{
		User:           "vinay",
		Port:           22,
		PrivateKeyPath: keyPath,
		KnownHostsPath: knownHostsPath,
		ProxyJumps:     []string{"jump-1.example.com:22", "jump-2.example.com:22"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"direct:jump-1.example.com:22",
		"proxy:jump-2.example.com:22",
		"proxy:target.example.com:22",
	}, calls)
}

func TestDial_KnownHostsRejection(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tempDir)
	knownKey := generatePublicKey(t)
	unknownKey := generatePublicKey(t)
	knownHostsPath := writeKnownHosts(t, tempDir, "target.example.com", knownKey)

	dialer := NewDefaultSSHDialer()
	cfg, err := dialer.buildClientConfig(DialOptions{
		User:           "vinay",
		Port:           22,
		PrivateKeyPath: keyPath,
		KnownHostsPath: knownHostsPath,
	})
	require.NoError(t, err)

	err = cfg.HostKeyCallback("target.example.com:22", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}, unknownKey)
	require.Error(t, err)
}

func writeTestPrivateKey(t *testing.T, dir string) string {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	path := filepath.Join(dir, "id_rsa")
	err = os.WriteFile(path, pemBytes, 0o600)
	require.NoError(t, err)
	return path
}

func generatePublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	return signer.PublicKey()
}

func writeKnownHosts(t *testing.T, dir, host string, pubKey ssh.PublicKey) string {
	t.Helper()

	line := knownhosts.Line([]string{host}, pubKey)
	path := filepath.Join(dir, "known_hosts")
	err := os.WriteFile(path, []byte(line+"\n"), 0o600)
	require.NoError(t, err)
	return path
}
