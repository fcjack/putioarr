package transfer_test

import (
	"testing"

	"github.com/italolelis/seedbox_downloader/internal/transfer"
)

func TestComputeStatus(t *testing.T) {
	t.Parallel()

	const size = int64(1000)

	tests := []struct {
		name            string
		putioStatus     string
		dbStatus        string
		size            int64
		localDownloaded int64
		existsOnPutio   bool
		want            transfer.CompositeStatus
	}{
		// Remote phase (Put.io still working).
		{name: "in_queue", putioStatus: "IN_QUEUE", existsOnPutio: true, want: transfer.StatusQueuedOnPutio},
		{name: "waiting", putioStatus: "waiting", existsOnPutio: true, want: transfer.StatusQueuedOnPutio},
		{name: "downloading remotely", putioStatus: "DOWNLOADING", existsOnPutio: true, want: transfer.StatusDownloadingOnPutio},
		{name: "completing remotely", putioStatus: "COMPLETING", existsOnPutio: true, want: transfer.StatusDownloadingOnPutio},
		{name: "putio error", putioStatus: "ERROR", existsOnPutio: true, want: transfer.StatusFailedPutio},

		// Local phase (Put.io copy available).
		{name: "available no db row, nothing on disk", putioStatus: "completed", existsOnPutio: true, size: size, want: transfer.StatusReadyForLocal},
		{name: "available no db row, partial disk", putioStatus: "completed", existsOnPutio: true, size: size, localDownloaded: 400, want: transfer.StatusDownloadingLocal},
		{name: "available no db row, full disk", putioStatus: "completed", existsOnPutio: true, size: size, localDownloaded: size, want: transfer.StatusLocalComplete},
		{name: "pending claimed", putioStatus: "seeding", dbStatus: "pending", existsOnPutio: true, size: size, want: transfer.StatusReadyForLocal},
		{name: "downloading locally partial", putioStatus: "completed", dbStatus: "downloading", existsOnPutio: true, size: size, localDownloaded: 500, want: transfer.StatusDownloadingLocal},
		{name: "downloading locally but disk full", putioStatus: "completed", dbStatus: "downloading", existsOnPutio: true, size: size, localDownloaded: size, want: transfer.StatusLocalComplete},
		{name: "downloaded waiting import", putioStatus: "seeding", dbStatus: "downloaded", existsOnPutio: true, size: size, localDownloaded: size, want: transfer.StatusWaitingImport},
		{name: "failed local", putioStatus: "completed", dbStatus: "failed", existsOnPutio: true, size: size, want: transfer.StatusFailedLocal},
		{name: "files missing while available", putioStatus: "completed", dbStatus: "missing", existsOnPutio: true, size: size, want: transfer.StatusMissingOnPutio},
		{name: "imported db status", putioStatus: "seeding", dbStatus: "imported", existsOnPutio: true, size: size, want: transfer.StatusImported},
		{name: "seeding db status", putioStatus: "seeding", dbStatus: "seeding", existsOnPutio: true, size: size, want: transfer.StatusSeeding},

		// Missing phase (transfer gone from Put.io).
		{name: "missing marked", dbStatus: "missing", existsOnPutio: false, want: transfer.StatusMissingOnPutio},
		{name: "downloaded then removed = cleaned up", dbStatus: "downloaded", existsOnPutio: false, want: transfer.StatusCleanedUp},
		{name: "pending row but gone = orphaned", dbStatus: "pending", existsOnPutio: false, want: transfer.StatusOrphaned},
		{name: "failed row but gone = orphaned", dbStatus: "failed", existsOnPutio: false, want: transfer.StatusOrphaned},
		{name: "no row and gone", dbStatus: "", existsOnPutio: false, want: transfer.StatusMissingOnPutio},
		{name: "cleaned_up row and gone", dbStatus: "cleaned_up", existsOnPutio: false, want: transfer.StatusCleanedUp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := transfer.ComputeStatus(tt.putioStatus, tt.dbStatus, tt.size, tt.localDownloaded, tt.existsOnPutio)
			if got != tt.want {
				t.Errorf("ComputeStatus(%q, %q, %d, %d, %v) = %q, want %q",
					tt.putioStatus, tt.dbStatus, tt.size, tt.localDownloaded, tt.existsOnPutio, got, tt.want)
			}
		})
	}
}
