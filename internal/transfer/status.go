package transfer

import "strings"

// CompositeStatus is the single source of truth for a transfer's stage across the
// Put.io -> NAS -> *arr pipeline. It merges the Put.io remote status, the local
// SQLite download status, and on-disk byte progress into one value the UI can render.
type CompositeStatus string

const (
	// StatusQueuedOnPutio means Put.io has accepted the transfer but has not started downloading it remotely.
	StatusQueuedOnPutio CompositeStatus = "queued_on_putio"
	// StatusDownloadingOnPutio means Put.io is still fetching the content from peers.
	StatusDownloadingOnPutio CompositeStatus = "downloading_on_putio"
	// StatusReadyForLocal means the Put.io copy is complete and the local NAS download can start.
	StatusReadyForLocal CompositeStatus = "ready_for_local"
	// StatusDownloadingLocal means putioarr is copying the files from Put.io to the local disk.
	StatusDownloadingLocal CompositeStatus = "downloading_local"
	// StatusLocalComplete means all bytes are present on local disk but import has not been confirmed.
	StatusLocalComplete CompositeStatus = "local_complete"
	// StatusWaitingImport means the local copy finished and putioarr is waiting for *arr to import it.
	StatusWaitingImport CompositeStatus = "waiting_import"
	// StatusImported means *arr reported the content as imported.
	StatusImported CompositeStatus = "imported"
	// StatusSeeding means the import is done and Put.io is still seeding to satisfy the seed ratio.
	StatusSeeding CompositeStatus = "seeding"
	// StatusCleanedUp means the transfer was imported and removed from Put.io.
	StatusCleanedUp CompositeStatus = "cleaned_up"
	// StatusFailedLocal means the local NAS download failed.
	StatusFailedLocal CompositeStatus = "failed_local"
	// StatusFailedPutio means Put.io reported an error for the remote transfer.
	StatusFailedPutio CompositeStatus = "failed_putio"
	// StatusMissingOnPutio means the transfer (or its files) disappeared from Put.io before download finished.
	StatusMissingOnPutio CompositeStatus = "missing_on_putio"
	// StatusOrphaned means a SQLite row exists with no matching Put.io transfer.
	StatusOrphaned CompositeStatus = "orphaned"
	// StatusStuck is a heuristic state set by callers when a transfer has not progressed for too long.
	StatusStuck CompositeStatus = "stuck"
)

// ComputeStatus derives the composite status from the Put.io remote status, the
// SQLite download status, the expected size, the bytes present on local disk, and
// whether the transfer still exists on Put.io. It is pure and side-effect free so
// it can be unit-tested exhaustively and reused by both the UI and the pipeline.
func ComputeStatus(putioStatus, dbStatus string, size, localDownloaded int64, existsOnPutio bool) CompositeStatus {
	p := strings.ToLower(strings.TrimSpace(putioStatus))
	d := strings.ToLower(strings.TrimSpace(dbStatus))

	if !existsOnPutio {
		return missingPhaseStatus(d)
	}

	if s, ok := remotePhaseStatus(p); ok {
		return s
	}

	return localPhaseStatus(d, size, localDownloaded)
}

// missingPhaseStatus classifies transfers that no longer exist on Put.io based on
// what SQLite still remembers about them.
func missingPhaseStatus(dbStatus string) CompositeStatus {
	switch dbStatus {
	case "missing":
		return StatusMissingOnPutio
	case "downloaded", "imported", "cleaned_up":
		// Local copy completed before the remote transfer was removed: the pipeline
		// cleaned it up after a successful import.
		return StatusCleanedUp
	case "":
		return StatusMissingOnPutio
	default:
		return StatusOrphaned
	}
}

// remotePhaseStatus reports the composite status while Put.io is still working on
// the remote copy. The boolean is false once the remote copy is available, handing
// control to the local phase.
func remotePhaseStatus(putioStatus string) (CompositeStatus, bool) {
	switch putioStatus {
	case "in_queue", "waiting", "preparing_download":
		return StatusQueuedOnPutio, true
	case "downloading":
		return StatusDownloadingOnPutio, true
	case "finishing", "checking", "completing":
		return StatusDownloadingOnPutio, true
	case "error":
		return StatusFailedPutio, true
	default:
		return "", false
	}
}

// localPhaseStatus reports the composite status once the Put.io copy is available
// and the local NAS pipeline is responsible for progress.
func localPhaseStatus(dbStatus string, size, localDownloaded int64) CompositeStatus {
	switch dbStatus {
	case "failed":
		return StatusFailedLocal
	case "missing":
		return StatusMissingOnPutio
	case "downloading":
		if size > 0 && localDownloaded >= size {
			return StatusLocalComplete
		}

		return StatusDownloadingLocal
	case "downloaded":
		return StatusWaitingImport
	case "imported":
		return StatusImported
	case "seeding":
		return StatusSeeding
	case "cleaned_up":
		return StatusCleanedUp
	case "pending":
		return StatusReadyForLocal
	default:
		return inferLocalStatusFromDisk(size, localDownloaded)
	}
}

// inferLocalStatusFromDisk infers the local stage purely from on-disk bytes when no
// SQLite row exists yet (e.g. a freshly completed Put.io transfer not yet claimed).
func inferLocalStatusFromDisk(size, localDownloaded int64) CompositeStatus {
	if size > 0 && localDownloaded >= size {
		return StatusLocalComplete
	}

	if localDownloaded > 0 {
		return StatusDownloadingLocal
	}

	return StatusReadyForLocal
}
