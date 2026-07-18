package transfer

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockTransferWorker is a testify/mock implementation of TransferWorker.
type MockTransferWorker struct {
	mock.Mock
}

func (m *MockTransferWorker) Execute(ctx context.Context, chunk Chunk, spec FileTransfer) error {
	args := m.Called(ctx, chunk, spec)
	return args.Error(0)
}
