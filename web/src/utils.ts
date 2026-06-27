export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) {
    return '0 B'
  }

  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / Math.pow(1024, exponent)

  return `${value.toFixed(exponent === 0 ? 0 : 1)} ${units[exponent]}`
}

export function formatSpeed(bytesPerSecond: number): string {
  if (!bytesPerSecond || bytesPerSecond <= 0) {
    return ''
  }

  return `${formatBytes(bytesPerSecond)}/s`
}

export function formatEta(seconds: number): string {
  if (!seconds || seconds <= 0) {
    return ''
  }

  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = Math.floor(seconds % 60)

  if (hours > 0) {
    return `${hours}h ${minutes}m`
  }

  if (minutes > 0) {
    return `${minutes}m ${secs}s`
  }

  return `${secs}s`
}

const STATUS_LABELS: Record<string, string> = {
  queued_on_putio: 'Queued on Put.io',
  downloading_on_putio: 'Downloading on Put.io',
  ready_for_local: 'Ready for local',
  downloading_local: 'Downloading locally',
  local_complete: 'Local complete',
  waiting_import: 'Waiting import',
  imported: 'Imported',
  seeding: 'Seeding',
  cleaned_up: 'Cleaned up',
  failed_local: 'Local failed',
  failed_putio: 'Put.io failed',
  missing_on_putio: 'Missing on Put.io',
  orphaned: 'Orphaned',
  stuck: 'Stuck',
}

export function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status
}

export function statusTone(status: string): string {
  if (status.startsWith('failed') || status === 'missing_on_putio' || status === 'orphaned' || status === 'stuck') {
    return 'danger'
  }

  if (status === 'imported' || status === 'cleaned_up' || status === 'seeding') {
    return 'success'
  }

  if (status === 'downloading_local' || status === 'downloading_on_putio') {
    return 'active'
  }

  return 'neutral'
}
