package transfer

// FileChunker splits a file into transferable chunks.
type FileChunker interface {
	Chunk(file FileInfo, cfg ChunkConfig) ([]Chunk, error)
}

// ChunkConfig controls chunk sizing behavior.
type ChunkConfig struct {
	MinSize    uint64
	MaxSize    uint64
	NumWorkers int
}

// FileInfo is the minimum file metadata needed for chunking.
type FileInfo struct {
	ID   string
	Size uint64
}

// ChunkState tracks a chunk lifecycle state.
type ChunkState string

const (
	ChunkPending    ChunkState = "pending"
	ChunkInProgress ChunkState = "in_progress"
	ChunkDone       ChunkState = "done"
	ChunkFailed     ChunkState = "failed"
)

// Chunk is a contiguous range within a file.
type Chunk struct {
	ID     string
	FileID string
	Offset uint64
	Length uint64
	Done   uint64
	State  ChunkState
}
