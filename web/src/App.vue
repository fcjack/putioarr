<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import {
  listTransfers,
  getTransfer,
  retryTransfer,
  cancelTransfer,
  deleteTransfer,
  type TransferView,
  type TransferDetail,
  type DeleteScopes,
} from '@/api'
import { statusLabel } from '@/utils'
import TransfersTable from '@/components/TransfersTable.vue'
import TransferDrawer from '@/components/TransferDrawer.vue'
import DeleteDialog from '@/components/DeleteDialog.vue'
import AdminPanel from '@/components/AdminPanel.vue'

const POLL_INTERVAL_MS = 5000

const STATUS_OPTIONS = [
  'queued_on_putio',
  'downloading_on_putio',
  'ready_for_local',
  'downloading_local',
  'local_complete',
  'waiting_import',
  'imported',
  'seeding',
  'cleaned_up',
  'failed_local',
  'failed_putio',
  'missing_on_putio',
  'orphaned',
]

const tab = ref<'transfers' | 'admin'>('transfers')
const transfers = ref<TransferView[]>([])
const filters = ref({ name: '', status: '' })
const loading = ref(false)
const error = ref('')
const notice = ref('')
const busyId = ref<string | null>(null)

const detail = ref<TransferDetail | null>(null)
const detailOpen = ref(false)
const detailLoading = ref(false)

const deleteTarget = ref<TransferView | null>(null)
const deleteBusy = ref(false)

let timer: number | undefined

async function refresh() {
  loading.value = true

  try {
    transfers.value = await listTransfers(filters.value)
    error.value = ''
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    loading.value = false
  }
}

async function openDetail(t: TransferView) {
  detailOpen.value = true
  detailLoading.value = true
  detail.value = null

  try {
    detail.value = await getTransfer(t.id)
  } catch (err) {
    error.value = (err as Error).message
    detailOpen.value = false
  } finally {
    detailLoading.value = false
  }
}

function closeDetail() {
  detailOpen.value = false
  detail.value = null
}

async function runAction(id: string, action: () => Promise<unknown>, message: string) {
  busyId.value = id

  try {
    await action()
    notice.value = message
    error.value = ''
    await refresh()
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    busyId.value = null
  }
}

function doRetry(id: string) {
  return runAction(id, () => retryTransfer(id), 'Transfer re-queued.')
}

function doCancel(id: string) {
  return runAction(id, () => cancelTransfer(id), 'Download cancelled.')
}

function requestDelete(t: TransferView) {
  deleteTarget.value = t
}

function requestDeleteById(id: string) {
  const target = transfers.value.find((t) => t.id === id) ?? detail.value
  if (target) {
    deleteTarget.value = target as TransferView
  }
}

async function confirmDelete(scopes: DeleteScopes) {
  if (!deleteTarget.value) {
    return
  }

  const id = deleteTarget.value.id
  deleteBusy.value = true

  try {
    await deleteTransfer(id, scopes)
    notice.value = 'Transfer deleted.'
    error.value = ''
    deleteTarget.value = null
    closeDetail()
    await refresh()
  } catch (err) {
    error.value = (err as Error).message
  } finally {
    deleteBusy.value = false
  }
}

onMounted(() => {
  refresh()
  timer = window.setInterval(() => {
    if (tab.value === 'transfers' && !deleteTarget.value) {
      refresh()
    }
  }, POLL_INTERVAL_MS)
})

onUnmounted(() => {
  if (timer) {
    window.clearInterval(timer)
  }
})
</script>

<template>
  <div class="app">
    <header class="app-header">
      <h1>putioarr <span class="version">download manager</span></h1>
    </header>

    <div class="tabs">
      <button :class="{ active: tab === 'transfers' }" @click="tab = 'transfers'">Transfers</button>
      <button :class="{ active: tab === 'admin' }" @click="tab = 'admin'">Admin</button>
    </div>

    <div v-if="error" class="error-banner">{{ error }}</div>
    <div v-if="notice" class="badge success" style="margin-bottom: 12px">{{ notice }}</div>

    <template v-if="tab === 'transfers'">
      <div class="toolbar">
        <input v-model="filters.name" placeholder="Search by name" @input="refresh" />
        <select v-model="filters.status" @change="refresh">
          <option value="">All statuses</option>
          <option v-for="s in STATUS_OPTIONS" :key="s" :value="s">{{ statusLabel(s) }}</option>
        </select>
        <div class="spacer"></div>
        <span v-if="loading" class="muted">Refreshing…</span>
        <button @click="refresh">Refresh</button>
      </div>

      <TransfersTable
        :transfers="transfers"
        :busy-id="busyId"
        @select="openDetail"
        @retry="doRetry"
        @cancel="doCancel"
        @delete="requestDelete"
      />
    </template>

    <AdminPanel
      v-else
      @done="(m) => { notice = m; error = '' }"
      @error="(m) => { error = m }"
    />

    <TransferDrawer
      v-if="detailOpen"
      :detail="detail"
      :loading="detailLoading"
      @close="closeDetail"
      @retry="doRetry"
      @cancel="doCancel"
      @delete="requestDeleteById"
    />

    <DeleteDialog
      v-if="deleteTarget"
      :name="deleteTarget.name"
      :busy="deleteBusy"
      @confirm="confirmDelete"
      @cancel="deleteTarget = null"
    />
  </div>
</template>
