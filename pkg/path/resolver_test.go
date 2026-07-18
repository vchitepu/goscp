package path

import (
	"sort"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePathSpec_Local(t *testing.T) {
	t.Parallel()

	ps := ParsePathSpec("/tmp/data.txt")
	assert.False(t, ps.IsRemote)
	assert.Equal(t, "/tmp/data.txt", ps.Path)
	assert.Equal(t, "", ps.Host)
	assert.Equal(t, "", ps.User)
	assert.Equal(t, 0, ps.Port)
}

func TestParsePathSpec_Remote(t *testing.T) {
	t.Parallel()

	ps := ParsePathSpec("example.com:/var/data.txt")
	assert.True(t, ps.IsRemote)
	assert.Equal(t, "example.com", ps.Host)
	assert.Equal(t, "/var/data.txt", ps.Path)
	assert.Equal(t, "", ps.User)
	assert.Equal(t, 22, ps.Port)
}

func TestParsePathSpec_WithUser(t *testing.T) {
	t.Parallel()

	ps := ParsePathSpec("vinay@example.com:/var/data.txt")
	assert.True(t, ps.IsRemote)
	assert.Equal(t, "example.com", ps.Host)
	assert.Equal(t, "vinay", ps.User)
	assert.Equal(t, "/var/data.txt", ps.Path)
	assert.Equal(t, 22, ps.Port)
}

func TestResolve_L2R(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/src/file.txt", []byte("abc"), 0o644))

	r := NewDefaultPathResolver(fs)
	tx, err := r.Resolve([]PathSpec{{Path: "/src/file.txt"}}, ParsePathSpec("remote:/dst/file.txt"))
	require.NoError(t, err)
	require.Len(t, tx, 1)

	assert.Equal(t, L2R, tx[0].Direction)
	assert.Equal(t, "/src/file.txt", tx[0].SrcPath)
	assert.Equal(t, "/dst/file.txt", tx[0].DstPath)
	assert.Equal(t, "", tx[0].SrcHost)
	assert.Equal(t, "remote", tx[0].DstHost)
	assert.Equal(t, uint64(3), tx[0].Size)
}

func TestResolve_R2L(t *testing.T) {
	t.Parallel()

	r := NewDefaultPathResolver(afero.NewMemMapFs())
	tx, err := r.Resolve([]PathSpec{ParsePathSpec("src:/var/a.txt")}, PathSpec{Path: "/tmp/a.txt"})
	require.NoError(t, err)
	require.Len(t, tx, 1)

	assert.Equal(t, R2L, tx[0].Direction)
	assert.Equal(t, "/var/a.txt", tx[0].SrcPath)
	assert.Equal(t, "/tmp/a.txt", tx[0].DstPath)
	assert.Equal(t, "src", tx[0].SrcHost)
	assert.Equal(t, "", tx[0].DstHost)
}

func TestResolve_R2R(t *testing.T) {
	t.Parallel()

	r := NewDefaultPathResolver(afero.NewMemMapFs())
	tx, err := r.Resolve(
		[]PathSpec{ParsePathSpec("src:/var/a.txt")},
		ParsePathSpec("dst:/backup/a.txt"),
	)
	require.NoError(t, err)
	require.Len(t, tx, 1)

	assert.Equal(t, R2R, tx[0].Direction)
	assert.Equal(t, "src", tx[0].SrcHost)
	assert.Equal(t, "dst", tx[0].DstHost)
	assert.Equal(t, "/var/a.txt", tx[0].SrcPath)
	assert.Equal(t, "/backup/a.txt", tx[0].DstPath)
}

func TestResolve_Directory(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/src/dir/sub", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/src/dir/a.txt", []byte("a"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/src/dir/sub/b.txt", []byte("bb"), 0o644))

	r := NewDefaultPathResolver(fs)
	tx, err := r.Resolve([]PathSpec{{Path: "/src/dir"}}, ParsePathSpec("remote:/dst/"))
	require.NoError(t, err)
	require.Len(t, tx, 2)

	sort.Slice(tx, func(i, j int) bool { return tx[i].SrcPath < tx[j].SrcPath })
	assert.Equal(t, "/src/dir/a.txt", tx[0].SrcPath)
	assert.Equal(t, "/dst/a.txt", tx[0].DstPath)
	assert.Equal(t, uint64(1), tx[0].Size)

	assert.Equal(t, "/src/dir/sub/b.txt", tx[1].SrcPath)
	assert.Equal(t, "/dst/sub/b.txt", tx[1].DstPath)
	assert.Equal(t, uint64(2), tx[1].Size)
}

func TestResolve_Glob(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/src", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/src/a.log", []byte("a"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/src/b.log", []byte("bb"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/src/c.txt", []byte("ccc"), 0o644))

	r := NewDefaultPathResolver(fs)
	tx, err := r.Resolve([]PathSpec{{Path: "/src/*.log"}}, ParsePathSpec("remote:/dst/"))
	require.NoError(t, err)
	require.Len(t, tx, 2)

	sort.Slice(tx, func(i, j int) bool { return tx[i].SrcPath < tx[j].SrcPath })
	assert.Equal(t, "/src/a.log", tx[0].SrcPath)
	assert.Equal(t, "/dst/a.log", tx[0].DstPath)
	assert.Equal(t, "/src/b.log", tx[1].SrcPath)
	assert.Equal(t, "/dst/b.log", tx[1].DstPath)
}
