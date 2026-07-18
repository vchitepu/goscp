package transfer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mb = uint64(1024 * 1024)

func TestChunk_LargeFile(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 1024 * mb}, ChunkConfig{NumWorkers: 4})
	require.NoError(t, err)
	require.Len(t, chunks, 4)

	for i, ch := range chunks {
		assert.Equal(t, "file-1", ch.FileID)
		assert.Equal(t, uint64(i)*256*mb, ch.Offset)
		assert.Equal(t, uint64(256*mb), ch.Length)
		assert.Equal(t, uint64(0), ch.Done)
		assert.Equal(t, ChunkPending, ch.State)
	}
}

func TestChunk_SmallFile(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 10 * mb}, ChunkConfig{NumWorkers: 8})
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, uint64(10*mb), chunks[0].Length)
}

func TestChunk_AutoSize(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 300 * mb}, ChunkConfig{NumWorkers: 10})
	require.NoError(t, err)
	require.Len(t, chunks, 5)

	total := uint64(0)
	for i, ch := range chunks {
		total += ch.Length
		if i < 4 {
			assert.Equal(t, uint64(64*mb), ch.Length)
		} else {
			assert.Equal(t, uint64(44*mb), ch.Length)
		}
	}
	assert.Equal(t, uint64(300*mb), total)
}

func TestChunk_ManualSize(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 100 * mb}, ChunkConfig{MinSize: 32 * mb, MaxSize: 32 * mb, NumWorkers: 10})
	require.NoError(t, err)
	require.Len(t, chunks, 4)
	assert.Equal(t, uint64(32*mb), chunks[0].Length)
	assert.Equal(t, uint64(32*mb), chunks[1].Length)
	assert.Equal(t, uint64(32*mb), chunks[2].Length)
	assert.Equal(t, uint64(4*mb), chunks[3].Length)
}

func TestChunk_SingleWorker(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 123 * mb}, ChunkConfig{NumWorkers: 1})
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	assert.Equal(t, uint64(123*mb), chunks[0].Length)
}

func TestChunk_ExactDivision(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 96 * mb}, ChunkConfig{MinSize: 32 * mb, MaxSize: 32 * mb, NumWorkers: 4})
	require.NoError(t, err)
	require.Len(t, chunks, 3)
	for _, ch := range chunks {
		assert.Equal(t, uint64(32*mb), ch.Length)
	}
}

func TestChunk_Remainder(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 100 * mb}, ChunkConfig{MinSize: 30 * mb, MaxSize: 30 * mb, NumWorkers: 4})
	require.NoError(t, err)
	require.Len(t, chunks, 4)
	assert.Equal(t, uint64(30*mb), chunks[0].Length)
	assert.Equal(t, uint64(30*mb), chunks[1].Length)
	assert.Equal(t, uint64(30*mb), chunks[2].Length)
	assert.Equal(t, uint64(10*mb), chunks[3].Length)
}

func TestChunk_ZeroLengthFile(t *testing.T) {
	t.Parallel()

	c := NewDefaultFileChunker()
	chunks, err := c.Chunk(FileInfo{ID: "file-1", Size: 0}, ChunkConfig{NumWorkers: 4})
	require.NoError(t, err)
	assert.Len(t, chunks, 0)
}
