package ssh

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	stderrs "errors"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

type directDialFn func(ctx context.Context, address string, cfg *ssh.ClientConfig) (*ssh.Client, error)
type proxyDialFn func(ctx context.Context, jumpClient *ssh.Client, address string, cfg *ssh.ClientConfig) (*ssh.Client, error)

// DefaultSSHDialer is a production SSHDialer backed by golang.org/x/crypto/ssh.
type DefaultSSHDialer struct {
	logger     *logrus.Entry
	directDial directDialFn
	proxyDial  proxyDialFn

	mu      sync.Mutex
	clients []*ssh.Client
}

// NewDefaultSSHDialer creates a dialer with secure known_hosts verification.
func NewDefaultSSHDialer() *DefaultSSHDialer {
	d := &DefaultSSHDialer{
		logger: logrus.WithField("component", "ssh_dialer"),
	}
	d.directDial = d.defaultDirectDial
	d.proxyDial = d.defaultProxyDial
	return d
}

// Dial dials a host directly or through one/many ProxyJump hops.
func (d *DefaultSSHDialer) Dial(ctx context.Context, host string, opts DialOptions) (*ssh.Client, error) {
	cfg, err := d.buildClientConfig(opts)
	if err != nil {
		return nil, err
	}

	port := opts.Port
	if port == 0 {
		port = 22
	}
	targetAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	d.logger.WithField("target", targetAddr).Debug("dial start")

	if len(opts.ProxyJumps) == 0 {
		client, dialErr := d.directDial(ctx, targetAddr, cfg)
		if dialErr != nil {
			return nil, dialErr
		}
		d.trackClient(client)
		return client, nil
	}

	firstJump := opts.ProxyJumps[0]
	client, dialErr := d.directDial(ctx, firstJump, cfg)
	if dialErr != nil {
		return nil, errors.Wrapf(dialErr, "dial first proxy jump %s", firstJump)
	}
	d.trackClient(client)

	for _, hop := range append(opts.ProxyJumps[1:], targetAddr) {
		nextClient, hopErr := d.proxyDial(ctx, client, hop, cfg)
		if hopErr != nil {
			return nil, errors.Wrapf(hopErr, "dial through proxy to %s", hop)
		}
		d.trackClient(nextClient)
		client = nextClient
	}

	return client, nil
}

// Close closes all tracked ssh clients.
func (d *DefaultSSHDialer) Close() error {
	d.mu.Lock()
	clients := d.clients
	d.clients = nil
	d.mu.Unlock()

	var errs []error
	for _, client := range clients {
		if client == nil {
			continue
		}
		if err := client.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return stderrs.Join(errs...)
	}
	return nil
}

func (d *DefaultSSHDialer) buildClientConfig(opts DialOptions) (*ssh.ClientConfig, error) {
	if opts.User == "" {
		return nil, errors.New("ssh user is required")
	}
	if opts.KnownHostsPath == "" {
		return nil, errors.New("known_hosts path is required")
	}

	authMethods, err := d.buildAuthMethods(opts)
	if err != nil {
		return nil, err
	}
	if len(authMethods) == 0 {
		return nil, errors.New("no auth methods configured")
	}

	hostKeyCallback, err := knownhosts.New(opts.KnownHostsPath)
	if err != nil {
		return nil, errors.Wrap(err, "create known_hosts callback")
	}

	cfg := &ssh.ClientConfig{
		User:            opts.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}
	if opts.Timeout > 0 {
		cfg.Timeout = opts.Timeout
	}

	return cfg, nil
}

func (d *DefaultSSHDialer) buildAuthMethods(opts DialOptions) ([]ssh.AuthMethod, error) {
	auth := make([]ssh.AuthMethod, 0, 2)

	if opts.PrivateKeyPath != "" {
		signer, err := loadPrivateKeySigner(opts.PrivateKeyPath)
		if err != nil {
			return nil, errors.Wrapf(err, "load private key %s", opts.PrivateKeyPath)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}

	agentAuth, err := buildSSHAgentAuth()
	if err != nil {
		d.logger.WithField("error", err.Error()).Debug("ssh agent auth unavailable")
	} else if agentAuth != nil {
		auth = append(auth, agentAuth)
	}

	return auth, nil
}

func (d *DefaultSSHDialer) defaultDirectDial(ctx context.Context, address string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (d *DefaultSSHDialer) defaultProxyDial(ctx context.Context, jumpClient *ssh.Client, address string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	if jumpClient == nil {
		return nil, errors.New("jump client is nil")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	conn, err := jumpClient.Dial("tcp", address)
	if err != nil {
		return nil, err
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, address, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (d *DefaultSSHDialer) trackClient(client *ssh.Client) {
	if client == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clients = append(d.clients, client)
}

func loadPrivateKeySigner(privateKeyPath string) (ssh.Signer, error) {
	absPath := privateKeyPath
	if !filepath.IsAbs(privateKeyPath) {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		absPath = filepath.Join(wd, privateKeyPath)
	}
	keyBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range keyBytes {
			keyBytes[i] = 0
		}
	}()
	return ssh.ParsePrivateKey(keyBytes)
}

func buildSSHAgentAuth() (ssh.AuthMethod, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK not set")
	}

	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil, err
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if len(signers) == 0 {
		_ = conn.Close()
		return nil, errors.New("ssh agent has no signers")
	}

	return ssh.PublicKeys(signers...), nil
}
