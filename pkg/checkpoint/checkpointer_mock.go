package checkpoint

import (
	"github.com/stretchr/testify/mock"
	"github.com/vchitepu/goscp/pkg/transfer"
)

// MockCheckpointer is a testify/mock implementation of Checkpointer.
type MockCheckpointer struct {
	mock.Mock
}

func (m *MockCheckpointer) Save(state CheckpointState) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockCheckpointer) Load(id string) (CheckpointState, error) {
	args := m.Called(id)
	state, _ := args.Get(0).(CheckpointState)
	return state, args.Error(1)
}

func (m *MockCheckpointer) UpdateChunk(id string, chunk transfer.Chunk) error {
	args := m.Called(id, chunk)
	return args.Error(0)
}

func (m *MockCheckpointer) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}
