package checkpoint

import (
	"time"

	"github.com/pkg/errors"
	"github.com/vchitepu/goscp/pkg/transfer"
)

var ErrCheckpointNotFound = errors.New("checkpoint not found")

// Checkpointer persists and restores transfer checkpoint state.
type Checkpointer interface {
	Save(state CheckpointState) error
	Load(id string) (CheckpointState, error)
	UpdateChunk(id string, chunk transfer.Chunk) error
	Delete(id string) error
}

// TransferSpec describes transfer sources and destination for checkpoint identity.
type TransferSpec struct {
	SrcPaths []string `json:"src_paths"`
	DstPath  string   `json:"dst_path"`
}

// CheckpointState is the persisted state required to resume transfers.
type CheckpointState struct {
	ID        string           `json:"id"`
	CreatedAt time.Time        `json:"created_at"`
	Spec      TransferSpec     `json:"spec"`
	Chunks    []transfer.Chunk `json:"chunks"`
}
