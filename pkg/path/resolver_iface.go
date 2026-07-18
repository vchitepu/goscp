package path

// PathResolver resolves source/destination specs to concrete file transfers.
type PathResolver interface {
	Resolve(srcs []PathSpec, dst PathSpec) ([]FileTransfer, error)
}

// PathSpec is a local or remote path descriptor.
type PathSpec struct {
	Path     string
	IsRemote bool
	Host     string
	User     string
	Port     int
}

// TransferDirection describes copy direction.
type TransferDirection string

const (
	L2R TransferDirection = "l2r"
	R2L TransferDirection = "r2l"
	R2R TransferDirection = "r2r"
)

// FileTransfer is a concrete copy operation.
type FileTransfer struct {
	SrcPath    string
	DstPath    string
	SrcHost    string
	DstHost    string
	Direction  TransferDirection
	Size       uint64
}
