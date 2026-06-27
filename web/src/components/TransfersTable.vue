<script setup lang="ts">
import type { TransferView } from '@/api'
import { formatBytes, formatSpeed, formatEta } from '@/utils'
import StatusBadge from './StatusBadge.vue'

defineProps<{ transfers: TransferView[]; busyId: string | null }>()

const emit = defineEmits<{
  (e: 'select', transfer: TransferView): void
  (e: 'retry', id: string): void
  (e: 'cancel', id: string): void
  (e: 'delete', transfer: TransferView): void
}>()

function canCancel(t: TransferView): boolean {
  return t.status === 'downloading_local'
}

function canRetry(t: TransferView): boolean {
  return t.status.startsWith('failed') || t.status === 'missing_on_putio' || t.status === 'orphaned'
}
</script>

<template>
  <div class="card">
    <table>
      <thead>
        <tr>
          <th>Name</th>
          <th>Status</th>
          <th>Progress</th>
          <th>Size</th>
          <th>Speed</th>
          <th style="text-align: right">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="t in transfers" :key="t.id" @click="emit('select', t)">
          <td>
            <div>{{ t.name }}</div>
            <div class="muted" style="font-size: 12px">{{ t.label }}</div>
          </td>
          <td><StatusBadge :status="t.status" /></td>
          <td>
            <div class="progress" :title="`${t.progress.toFixed(1)}%`">
              <span :style="{ width: `${Math.min(t.progress, 100)}%` }"></span>
            </div>
            <span class="muted" style="font-size: 12px">
              {{ t.progress.toFixed(0) }}%<template v-if="formatEta(t.eta)"> · {{ formatEta(t.eta) }}</template>
            </span>
          </td>
          <td>{{ formatBytes(t.size) }}</td>
          <td class="muted">{{ formatSpeed(t.downloadSpeed) || '—' }}</td>
          <td @click.stop>
            <div class="row-actions">
              <button v-if="canRetry(t)" :disabled="busyId === t.id" @click="emit('retry', t.id)">Retry</button>
              <button v-if="canCancel(t)" :disabled="busyId === t.id" @click="emit('cancel', t.id)">Cancel</button>
              <button class="danger" :disabled="busyId === t.id" @click="emit('delete', t)">Delete</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    <div v-if="transfers.length === 0" class="empty">No transfers found.</div>
  </div>
</template>
