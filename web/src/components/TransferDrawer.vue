<script setup lang="ts">
import type { TransferDetail } from '@/api'
import { formatBytes, formatSpeed } from '@/utils'
import StatusBadge from './StatusBadge.vue'

defineProps<{ detail: TransferDetail | null; loading: boolean }>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'retry', id: string): void
  (e: 'cancel', id: string): void
  (e: 'delete', id: string): void
}>()
</script>

<template>
  <div class="drawer-backdrop" @click.self="emit('close')">
    <aside class="drawer">
      <div class="toolbar">
        <button @click="emit('close')">← Back</button>
        <div class="spacer"></div>
      </div>

      <div v-if="loading" class="muted">Loading…</div>

      <template v-else-if="detail">
        <h2>{{ detail.name }}</h2>
        <StatusBadge :status="detail.status" />

        <dl class="detail-grid">
          <dt>Transfer ID</dt>
          <dd>{{ detail.id }}</dd>
          <dt>Put.io status</dt>
          <dd>{{ detail.putioStatus || '—' }}</dd>
          <dt>Local status</dt>
          <dd>{{ detail.localStatus || '—' }}</dd>
          <dt>Size</dt>
          <dd>{{ formatBytes(detail.size) }}</dd>
          <dt>Downloaded</dt>
          <dd>{{ formatBytes(detail.downloaded) }} ({{ detail.progress.toFixed(1) }}%)</dd>
          <dt>Speed</dt>
          <dd>{{ formatSpeed(detail.downloadSpeed) || '—' }}</dd>
          <dt>Save path</dt>
          <dd>{{ detail.savePath || '—' }}</dd>
          <dt v-if="detail.errorMessage">Error</dt>
          <dd v-if="detail.errorMessage" class="badge danger">{{ detail.errorMessage }}</dd>
        </dl>

        <h3 style="font-size: 14px">Pipeline</h3>
        <ul class="timeline">
          <li
            v-for="event in detail.timeline"
            :key="event.stage"
            :class="{ reached: event.reached, current: event.current }"
          >
            <span class="dot"></span>
            <span>{{ event.label }}</span>
          </li>
        </ul>

        <h3 style="font-size: 14px">Files ({{ detail.files.length }})</h3>
        <ul class="muted" style="padding-left: 18px">
          <li v-for="file in detail.files" :key="file.path">
            {{ file.path }} — {{ formatBytes(file.size) }}
          </li>
          <li v-if="detail.files.length === 0">No files available.</li>
        </ul>

        <div class="actions" style="margin-top: 20px">
          <button @click="emit('retry', detail.id)">Retry</button>
          <button @click="emit('cancel', detail.id)">Cancel download</button>
          <button class="danger" @click="emit('delete', detail.id)">Delete…</button>
        </div>
      </template>

      <div v-else class="muted">Transfer not found.</div>
    </aside>
  </div>
</template>
