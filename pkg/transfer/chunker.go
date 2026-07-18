package transfer

const (
	defaultMinChunkSize uint64 = 64 * 1024 * 1024
	defaultMaxChunkSize uint64 = 512 * 1024 * 1024
)

// DefaultFileChunker implements FileChunker using fixed-size contiguous chunks.
type DefaultFileChunker struct{}

func NewDefaultFileChunker() *DefaultFileChunker {
	return &DefaultFileChunker{}
}

func (c *DefaultFileChunker) Chunk(file FileInfo, cfg ChunkConfig) ([]Chunk, error) {
	if file.Size == 0 {
		return []Chunk{}, nil
	}

	workers := cfg.NumWorkers
	if workers <= 0 {
		workers = 1
	}

	minSize := cfg.MinSize
	if minSize == 0 {
		minSize = defaultMinChunkSize
	}

	maxSize := cfg.MaxSize
	if maxSize == 0 {
		maxSize = defaultMaxChunkSize
	}

	if minSize > maxSize {
		minSize, maxSize = maxSize, minSize
	}

	autoSize := ceilDiv(file.Size, uint64(workers))
	chunkSize := clamp(autoSize, minSize, maxSize)
	if chunkSize == 0 {
		chunkSize = minSize
	}

	chunks := make([]Chunk, 0, ceilDiv(file.Size, chunkSize))
	for offset := uint64(0); offset < file.Size; offset += chunkSize {
		length := chunkSize
		if remaining := file.Size - offset; remaining < chunkSize {
			length = remaining
		}
		chunks = append(chunks, Chunk{
			FileID: file.ID,
			Offset: offset,
			Length: length,
			Done:   0,
			State:  ChunkPending,
		})
	}

	return chunks, nil
}

func ceilDiv(n, d uint64) uint64 {
	if d == 0 {
		return 0
	}
	return (n + d - 1) / d
}

func clamp(v, minV, maxV uint64) uint64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}
