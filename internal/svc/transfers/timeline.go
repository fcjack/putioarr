package transfers

import "github.com/italolelis/seedbox_downloader/internal/transfer"

// TimelineEvent is a single point in a transfer's lifecycle, derived from the
// composite status and the available SQLite timestamp. Until a richer event store
// exists, the timeline is reconstructed from current state rather than persisted history.
type TimelineEvent struct {
	Stage     string `json:"stage"`
	Label     string `json:"label"`
	Reached   bool   `json:"reached"`
	Current   bool   `json:"current"`
	Timestamp string `json:"timestamp,omitempty"`
}

// pipelineStages is the happy-path ordering used to render progress in the UI.
var pipelineStages = []struct {
	stage  transfer.CompositeStatus
	label  string
	weight int
}{
	{transfer.StatusQueuedOnPutio, "Queued on Put.io", 1},
	{transfer.StatusDownloadingOnPutio, "Downloading on Put.io", 2},
	{transfer.StatusReadyForLocal, "Ready for local download", 3},
	{transfer.StatusDownloadingLocal, "Downloading locally", 4},
	{transfer.StatusLocalComplete, "Local download complete", 5},
	{transfer.StatusWaitingImport, "Waiting for import", 6},
	{transfer.StatusImported, "Imported", 7},
	{transfer.StatusSeeding, "Seeding", 8},
	{transfer.StatusCleanedUp, "Cleaned up", 9},
}

// buildTimeline reconstructs the pipeline timeline for a transfer view.
func buildTimeline(view TransferView) []TimelineEvent {
	current := transfer.CompositeStatus(view.Status)

	if event, ok := terminalTimeline(view, current); ok {
		return event
	}

	currentWeight := stageWeight(current)

	events := make([]TimelineEvent, 0, len(pipelineStages))

	for _, stage := range pipelineStages {
		event := TimelineEvent{
			Stage:   string(stage.stage),
			Label:   stage.label,
			Reached: stage.weight <= currentWeight,
			Current: stage.stage == current,
		}

		if event.Current {
			event.Timestamp = view.DownloadedAt
		}

		events = append(events, event)
	}

	return events
}

// terminalTimeline returns a single-event timeline for failure/terminal states that
// are not part of the linear happy path.
func terminalTimeline(view TransferView, current transfer.CompositeStatus) ([]TimelineEvent, bool) {
	labels := map[transfer.CompositeStatus]string{
		transfer.StatusFailedLocal:    "Local download failed",
		transfer.StatusFailedPutio:    "Put.io transfer failed",
		transfer.StatusMissingOnPutio: "Missing on Put.io",
		transfer.StatusOrphaned:       "Orphaned database record",
		transfer.StatusStuck:          "Stuck",
	}

	label, ok := labels[current]
	if !ok {
		return nil, false
	}

	return []TimelineEvent{{
		Stage:     string(current),
		Label:     label,
		Reached:   true,
		Current:   true,
		Timestamp: view.DownloadedAt,
	}}, true
}

func stageWeight(status transfer.CompositeStatus) int {
	for _, stage := range pipelineStages {
		if stage.stage == status {
			return stage.weight
		}
	}

	return 0
}
