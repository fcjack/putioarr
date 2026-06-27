<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getConfig, resetDatabase, purgeDownloads, type ConfigSnapshot } from '@/api'
import ConfirmDialog from './ConfirmDialog.vue'

const config = ref<ConfigSnapshot | null>(null)
const error = ref('')
const busy = ref(false)
const pending = ref<null | 'reset' | 'purge'>(null)

const emit = defineEmits<{ (e: 'done', message: string): void; (e: 'error', message: string): void }>()

onMounted(async () => {
  try {
    config.value = await getConfig()
  } catch (err) {
    error.value = (err as Error).message
  }
})

async function runPending() {
  if (!pending.value) {
    return
  }

  busy.value = true

  try {
    if (pending.value === 'reset') {
      await resetDatabase()
      emit('done', 'Database reset complete.')
    } else {
      await purgeDownloads()
      emit('done', 'Download directory purged.')
    }
  } catch (err) {
    emit('error', (err as Error).message)
  } finally {
    busy.value = false
    pending.value = null
  }
}
</script>

<template>
  <div class="card" style="padding: 16px">
    <div v-if="error" class="error-banner">{{ error }}</div>

    <div v-if="config" class="admin-section">
      <h3>Configuration</h3>
      <dl class="detail-grid">
        <dt>Version</dt>
        <dd>{{ config.version }}</dd>
        <dt>Download client</dt>
        <dd>{{ config.downloadClient }}</dd>
        <dt>Target label</dt>
        <dd>{{ config.targetLabel || '—' }}</dd>
        <dt>Download dir</dt>
        <dd>{{ config.downloadDir }}</dd>
        <dt>Max parallel</dt>
        <dd>{{ config.maxParallel }}</dd>
        <dt>Polling</dt>
        <dd>{{ config.pollingInterval }}</dd>
        <dt>Seed ratio</dt>
        <dd>{{ config.putioSeedRatio }}</dd>
        <dt>Sonarr / Radarr</dt>
        <dd>
          {{ config.sonarrConfigured ? 'Sonarr ✓' : 'Sonarr ✗' }} ·
          {{ config.radarrConfigured ? 'Radarr ✓' : 'Radarr ✗' }}
        </dd>
      </dl>
    </div>

    <div class="admin-section">
      <h3>Danger zone</h3>
      <p class="muted">Destructive operations require confirmation.</p>
      <div class="actions">
        <button class="danger" @click="pending = 'reset'">Reset database</button>
        <button class="danger" @click="pending = 'purge'">Purge download directory</button>
      </div>
    </div>

    <ConfirmDialog
      v-if="pending === 'reset'"
      title="Reset database?"
      message="This wipes all transfer tracking rows from SQLite. Active downloads may be re-detected on the next poll."
      confirm-label="Reset database"
      :busy="busy"
      @confirm="runPending"
      @cancel="pending = null"
    />

    <ConfirmDialog
      v-if="pending === 'purge'"
      title="Purge download directory?"
      message="This deletes all files under the download directory. This cannot be undone."
      confirm-label="Purge directory"
      :busy="busy"
      @confirm="runPending"
      @cancel="pending = null"
    />
  </div>
</template>
