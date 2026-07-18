package transfer

import "github.com/stretchr/testify/mock"

// MockFileChunker is a testify/mock implementation of FileChunker.
type MockFileChunker struct {
	mock.Mock
}

func (m *MockFileChunker) Chunk(file FileInfo, cfg ChunkConfig) ([]Chunk, error) {
	args := m.Called(file, cfg)
	chunks, _ := args.Get(0).([]Chunk)
	return chunks, args.Error(1)
}
