package path

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/afero"
)

// DefaultPathResolver resolves local/remote path specs.
type DefaultPathResolver struct {
	fs afero.Fs
}

func NewDefaultPathResolver(fs afero.Fs) *DefaultPathResolver {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	return &DefaultPathResolver{fs: fs}
}

// ParsePathSpec parses local paths and [user@]host:path remote specs.
func ParsePathSpec(raw string) PathSpec {
	if isRemoteSpec(raw) {
		userHost, p, _ := strings.Cut(raw, ":")
		user := ""
		host := userHost
		if strings.Contains(userHost, "@") {
			user, host, _ = strings.Cut(userHost, "@")
		}
		return PathSpec{Path: p, IsRemote: true, Host: host, User: user, Port: 22}
	}
	return PathSpec{Path: raw}
}

func isRemoteSpec(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/") {
		return false
	}
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return false
	}
	left := s[:colon]
	return left != ""
}

func (r *DefaultPathResolver) Resolve(srcs []PathSpec, dst PathSpec) ([]FileTransfer, error) {
	if len(srcs) == 0 {
		return nil, errors.New("at least one source is required")
	}

	out := make([]FileTransfer, 0)
	for _, src := range srcs {
		expanded, err := r.expandSource(src)
		if err != nil {
			return nil, err
		}
		for _, file := range expanded {
			transfer := FileTransfer{
				SrcPath:   file.Path,
				DstPath:   r.resolveDestinationPath(file.Path, src.Path, dst.Path, file.FromDir),
				SrcHost:   src.Host,
				DstHost:   dst.Host,
				Direction: inferDirection(src, dst),
				Size:      file.Size,
			}
			out = append(out, transfer)
		}
	}

	return out, nil
}

type resolvedSourceFile struct {
	Path    string
	Size    uint64
	FromDir bool
}

func (r *DefaultPathResolver) expandSource(src PathSpec) ([]resolvedSourceFile, error) {
	if src.IsRemote {
		return []resolvedSourceFile{{Path: src.Path, Size: 0, FromDir: false}}, nil
	}

	if hasGlob(src.Path) {
		matches, err := afero.Glob(r.fs, src.Path)
		if err != nil {
			return nil, err
		}
		files := make([]resolvedSourceFile, 0, len(matches))
		for _, m := range matches {
			st, err := r.fs.Stat(m)
			if err != nil {
				return nil, err
			}
			if st.IsDir() {
				continue
			}
			files = append(files, resolvedSourceFile{Path: m, Size: uint64(st.Size())})
		}
		return files, nil
	}

	st, err := r.fs.Stat(src.Path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []resolvedSourceFile{{Path: src.Path, Size: uint64(st.Size())}}, nil
	}

	base := src.Path
	files := make([]resolvedSourceFile, 0)
	err = afero.Walk(r.fs, base, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, resolvedSourceFile{Path: path, Size: uint64(info.Size()), FromDir: true})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func hasGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func inferDirection(src, dst PathSpec) TransferDirection {
	if src.IsRemote && dst.IsRemote {
		return R2R
	}
	if src.IsRemote {
		return R2L
	}
	return L2R
}

func (r *DefaultPathResolver) resolveDestinationPath(srcPath, srcSpecPath, dstPath string, fromDir bool) string {
	dstIsDir := strings.HasSuffix(dstPath, "/")
	if !dstIsDir {
		if st, err := r.fs.Stat(dstPath); err == nil && st.IsDir() {
			dstIsDir = true
		}
	}

	if fromDir {
		base := strings.TrimSuffix(srcSpecPath, "/")
		rel, err := filepath.Rel(base, srcPath)
		if err == nil {
			return filepath.Join(dstPath, rel)
		}
	}

	if dstIsDir {
		return filepath.Join(dstPath, filepath.Base(srcPath))
	}
	return dstPath
}
