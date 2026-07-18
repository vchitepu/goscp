package ssh

import (
	"context"

	"github.com/stretchr/testify/mock"
	gssh "golang.org/x/crypto/ssh"
)

// MockSSHDialer is a testify/mock implementation of SSHDialer.
type MockSSHDialer struct {
	mock.Mock
}

func (m *MockSSHDialer) Dial(ctx context.Context, host string, opts DialOptions) (*gssh.Client, error) {
	args := m.Called(ctx, host, opts)
	client, _ := args.Get(0).(*gssh.Client)
	return client, args.Error(1)
}

func (m *MockSSHDialer) Close() error {
	args := m.Called()
	return args.Error(0)
}
