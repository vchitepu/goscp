package ssh

import (
	"context"
	"time"

	gssh "golang.org/x/crypto/ssh"
)

// SSHDialer dials SSH hosts directly or through ProxyJump hops.
type SSHDialer interface {
	Dial(ctx context.Context, host string, opts DialOptions) (*gssh.Client, error)
	Close() error
}

// DialOptions defines dial/auth/security settings for SSH connections.
type DialOptions struct {
	User           string
	Port           int
	PrivateKeyPath string
	KnownHostsPath string
	ProxyJumps     []string
	Timeout        time.Duration
}
