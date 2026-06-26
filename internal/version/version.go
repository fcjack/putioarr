// Package version holds build metadata injected at compile time.
package version

import "fmt"

// Set at build time via -ldflags; defaults suit local go run / go test.
var (
	Version   = "1.4.1-dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns a single-line version identifier for logs and CLI output.
func String() string {
	if Commit != "unknown" && len(Commit) > 7 {
		return fmt.Sprintf("%s (%s)", Version, Commit[:7])
	}

	return Version
}

// Info returns full build metadata.
func Info() string {
	return fmt.Sprintf("version=%s commit=%s built=%s", Version, Commit, BuildTime)
}
