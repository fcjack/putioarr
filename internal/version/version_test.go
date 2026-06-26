package version

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestString(t *testing.T) {
	t.Parallel()

	origVersion, origCommit := Version, Commit
	t.Cleanup(func() {
		Version, Commit = origVersion, origCommit
	})

	Version = "1.2.3"
	Commit = "abcdef123456"
	require.Equal(t, "1.2.3 (abcdef1)", String())

	Commit = "unknown"
	require.Equal(t, "1.2.3", String())
}

func TestInfo(t *testing.T) {
	t.Parallel()

	origVersion, origCommit, origBuildTime := Version, Commit, BuildTime
	t.Cleanup(func() {
		Version, Commit, BuildTime = origVersion, origCommit, origBuildTime
	})

	Version = "2.0.0"
	Commit = "deadbeef"
	BuildTime = "2026-06-26T00:00:00Z"

	info := Info()
	require.True(t, strings.Contains(info, "version=2.0.0"))
	require.True(t, strings.Contains(info, "commit=deadbeef"))
	require.True(t, strings.Contains(info, "built=2026-06-26T00:00:00Z"))
}
