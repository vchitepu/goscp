package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/afero"
	"github.com/vchitepu/goscp/pkg/transfer"
)

const checkpointDirPerm = 0o700

// validCheckpointID matches only the hex strings produced by ComputeCheckpointID.
var validCheckpointID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// sanitizeID returns an error if the id would escape the checkpoint directory
// (e.g. "../../../etc/passwd" supplied via --resume flag).
func sanitizeID(id string) error {
	if !validCheckpointID.MatchString(id) {
		return errors.New("invalid checkpoint ID: must be 16 hex characters")
	}
	return nil
}

// FileCheckpointer stores checkpoint states as JSON files on an afero filesystem.
type FileCheckpointer struct {
	fs            afero.Fs
	checkpointDir string
}

func NewFileCheckpointer(fs afero.Fs, homeDir string) *FileCheckpointer {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return &FileCheckpointer{
		fs:            fs,
		checkpointDir: filepath.Join(homeDir, ".goscp", "checkpoints"),
	}
}

func (c *FileCheckpointer) Save(state CheckpointState) error {
	if state.ID == "" {
		state.ID = ComputeCheckpointID(state.Spec, state.CreatedAt)
	}
	if err := c.fs.MkdirAll(c.checkpointDir, checkpointDirPerm); err != nil {
		return errors.Wrapf(err, "ensure checkpoint directory %q", c.checkpointDir)
	}

	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return errors.Wrapf(err, "marshal checkpoint %q", state.ID)
	}

	if err := afero.WriteFile(c.fs, c.pathForID(state.ID), payload, 0o600); err != nil {
		return errors.Wrapf(err, "write checkpoint %q", state.ID)
	}

	return nil
}

func (c *FileCheckpointer) Load(id string) (CheckpointState, error) {
	if err := sanitizeID(id); err != nil {
		return CheckpointState{}, err
	}
	payload, err := afero.ReadFile(c.fs, c.pathForID(id))
	if err != nil {
		if isNotExist(err) {
			return CheckpointState{}, errors.Wrapf(ErrCheckpointNotFound, "%s", id)
		}
		return CheckpointState{}, errors.Wrapf(err, "read checkpoint %q", id)
	}

	var state CheckpointState
	if err := json.Unmarshal(payload, &state); err != nil {
		return CheckpointState{}, errors.Wrapf(err, "unmarshal checkpoint %q", id)
	}
	return state, nil
}

func (c *FileCheckpointer) UpdateChunk(id string, chunk transfer.Chunk) error {
	state, err := c.Load(id)
	if err != nil {
		return errors.Wrapf(err, "load checkpoint %q for chunk update", id)
	}

	replaced := false
	for i := range state.Chunks {
		if state.Chunks[i].ID == chunk.ID {
			state.Chunks[i] = chunk
			replaced = true
			break
		}
	}
	if !replaced {
		state.Chunks = append(state.Chunks, chunk)
	}

	if err := c.Save(state); err != nil {
		return errors.Wrapf(err, "save checkpoint %q after chunk update", id)
	}

	return nil
}

func (c *FileCheckpointer) Delete(id string) error {
	if err := sanitizeID(id); err != nil {
		return err
	}
	err := c.fs.Remove(c.pathForID(id))
	if err != nil && !isNotExist(err) {
		return errors.Wrapf(err, "delete checkpoint %q", id)
	}
	return nil
}

func (c *FileCheckpointer) pathForID(id string) string {
	return filepath.Join(c.checkpointDir, id+".json")
}

// ComputeCheckpointID returns sha256(src_paths + dst_path + creation_timestamp)[:16].
func ComputeCheckpointID(spec TransferSpec, createdAt time.Time) string {
	base := strings.Join(spec.SrcPaths, "|") + "|" + spec.DstPath + "|" + createdAt.UTC().Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])[:16]
}

func isNotExist(err error) bool {
	return os.IsNotExist(err)
}
