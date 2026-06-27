package transfer

import (
	"os"
	"path/filepath"
)

// LocalDownloadedBytes returns the number of bytes already present on local disk for
// a transfer, capped at each file's expected size. It is used to report local download
// progress and to derive the composite status. A zero downloadDir or fileless transfer
// yields 0.
func LocalDownloadedBytes(downloadDir string, t *Transfer) int64 {
	if downloadDir == "" || t == nil || len(t.Files) == 0 {
		return 0
	}

	var downloaded int64

	for _, file := range t.Files {
		info, err := os.Stat(filepath.Join(downloadDir, file.Path))
		if err != nil {
			continue
		}

		size := info.Size()
		if file.Size > 0 && size > file.Size {
			size = file.Size
		}

		downloaded += size
	}

	return downloaded
}
