package checkpoint

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vchitepu/goscp/pkg/transfer"
)

func TestSave_NewCheckpoint(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	cp := NewFileCheckpointer(fs, "/home/tester")

	createdAt := time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC)
	state := newTestState(createdAt)
	state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)

	require.NoError(t, cp.Save(state))

	checkpointPath := filepath.Join("/home/tester", ".goscp", "checkpoints", state.ID+".json")
	exists, err := afero.Exists(fs, checkpointPath)
	require.NoError(t, err)
	assert.True(t, exists)

	loaded, err := cp.Load(state.ID)
	require.NoError(t, err)
	assert.Equal(t, state.ID, loaded.ID)
	assert.Equal(t, state.Spec, loaded.Spec)
	require.Len(t, loaded.Chunks, 2)
}

func TestLoad_Existing(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	cp := NewFileCheckpointer(fs, "/home/tester")

	createdAt := time.Date(2026, 5, 25, 9, 0, 0, 0, time.UTC)
	state := newTestState(createdAt)
	state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)
	require.NoError(t, cp.Save(state))

	loaded, err := cp.Load(state.ID)
	require.NoError(t, err)

	assert.Equal(t, state.ID, loaded.ID)
	assert.Equal(t, state.CreatedAt.UTC(), loaded.CreatedAt.UTC())
	assert.Equal(t, state.Spec, loaded.Spec)
	assert.Equal(t, state.Chunks, loaded.Chunks)
}

func TestLoad_Missing(t *testing.T) {
	t.Parallel()

	cp := NewFileCheckpointer(afero.NewMemMapFs(), "/home/tester")

	_, err := cp.Load("deadbeefdeadbeef") // valid format, file does not exist
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCheckpointNotFound)
}

func TestLoad_InvalidID(t *testing.T) {
	t.Parallel()

	cp := NewFileCheckpointer(afero.NewMemMapFs(), "/home/tester")

	_, err := cp.Load("missing") // invalid format, should fail validation before filesystem lookup
	require.Error(t, err)
	assert.EqualError(t, err, "invalid checkpoint ID: must be 16 hex characters")
	assert.NotErrorIs(t, err, ErrCheckpointNotFound)
}

func TestUpdateChunk_Progress(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	cp := NewFileCheckpointer(fs, "/home/tester")

	createdAt := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	state := newTestState(createdAt)
	state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)
	require.NoError(t, cp.Save(state))

	updated := transfer.Chunk{ID: "chunk-2", FileID: "file-1", Offset: 512, Length: 512, Done: 512, State: transfer.ChunkDone}
	require.NoError(t, cp.UpdateChunk(state.ID, updated))

	loaded, err := cp.Load(state.ID)
	require.NoError(t, err)
	require.Len(t, loaded.Chunks, 2)

	assert.Equal(t, transfer.ChunkPending, loaded.Chunks[0].State)
	assert.Equal(t, uint64(0), loaded.Chunks[0].Done)
	assert.Equal(t, transfer.ChunkDone, loaded.Chunks[1].State)
	assert.Equal(t, uint64(512), loaded.Chunks[1].Done)
}

func TestDelete(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	cp := NewFileCheckpointer(fs, "/home/tester")

	state := newTestState(time.Date(2026, 5, 25, 11, 0, 0, 0, time.UTC))
	state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)
	require.NoError(t, cp.Save(state))
	require.NoError(t, cp.Delete(state.ID))

	_, err := cp.Load(state.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCheckpointNotFound)
}

func TestResume_PartialTransfer(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	cp := NewFileCheckpointer(fs, "/home/tester")

	state := CheckpointState{
		CreatedAt: time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC),
		Spec: TransferSpec{
			SrcPaths: []string{"/src/a.bin", "/src/b.bin"},
			DstPath:  "/dst/",
		},
		Chunks: []transfer.Chunk{
			{ID: "chunk-1", FileID: "file-1", Offset: 0, Length: 512, Done: 512, State: transfer.ChunkDone},
			{ID: "chunk-2", FileID: "file-1", Offset: 512, Length: 512, Done: 0, State: transfer.ChunkPending},
			{ID: "chunk-3", FileID: "file-2", Offset: 0, Length: 256, Done: 128, State: transfer.ChunkInProgress},
		},
	}
	state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)
	require.NoError(t, cp.Save(state))

	loaded, err := cp.Load(state.ID)
	require.NoError(t, err)

	remaining := make([]transfer.Chunk, 0)
	for _, ch := range loaded.Chunks {
		if ch.State != transfer.ChunkDone {
			remaining = append(remaining, ch)
		}
	}

	require.Len(t, remaining, 2)
	assert.Equal(t, "chunk-2", remaining[0].ID)
	assert.Equal(t, "chunk-3", remaining[1].ID)
}

func newTestState(createdAt time.Time) CheckpointState {
	return CheckpointState{
		CreatedAt: createdAt,
		Spec: TransferSpec{
			SrcPaths: []string{"/src/file.bin"},
			DstPath:  "/dst/file.bin",
		},
		Chunks: []transfer.Chunk{
			{ID: "chunk-1", FileID: "file-1", Offset: 0, Length: 512, Done: 0, State: transfer.ChunkPending},
			{ID: "chunk-2", FileID: "file-1", Offset: 512, Length: 512, Done: 0, State: transfer.ChunkPending},
		},
	}
}
