package transfer

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockTransferScheduler is a testify/mock implementation of TransferScheduler.
type MockTransferScheduler struct {
	mock.Mock
}

func (m *MockTransferScheduler) Schedule(ctx context.Context, chunks []Chunk, spec FileTransfer, factory WorkerFactory) <-chan Result {
	args := m.Called(ctx, chunks, spec, factory)
	ch, _ := args.Get(0).(<-chan Result)
	return ch
}
