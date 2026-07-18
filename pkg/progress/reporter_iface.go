package progress

import "github.com/vchitepu/goscp/pkg/transfer"

// Type aliases so progress package uses transfer domain models.
type FileInfo = transfer.FileInfo
type Result = transfer.Result

// ProgressReporter tracks transfer progress lifecycle.
type ProgressReporter interface {
	Add(f FileInfo)
	Update(r Result)
	Complete()
	Fail(f FileInfo, err error)
}
