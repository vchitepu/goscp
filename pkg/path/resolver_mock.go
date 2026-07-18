package path

import "github.com/stretchr/testify/mock"

// MockPathResolver is a testify/mock implementation of PathResolver.
type MockPathResolver struct {
	mock.Mock
}

func (m *MockPathResolver) Resolve(srcs []PathSpec, dst PathSpec) ([]FileTransfer, error) {
	args := m.Called(srcs, dst)
	transfers, _ := args.Get(0).([]FileTransfer)
	return transfers, args.Error(1)
}
