<script setup lang="ts">
import { reactive } from 'vue'
import type { DeleteScopes } from '@/api'

defineProps<{ name: string; busy?: boolean }>()

const emit = defineEmits<{
  (e: 'confirm', scopes: DeleteScopes): void
  (e: 'cancel'): void
}>()

const scopes = reactive<DeleteScopes>({ putio: true, local: true, db: true })

function confirm() {
  emit('confirm', { ...scopes })
}
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal" role="dialog" aria-modal="true">
      <h3>Delete transfer</h3>
      <p class="muted">Choose what to remove for "{{ name }}". This cannot be undone.</p>

      <div class="checkbox-row" style="flex-direction: column; gap: 8px">
        <label><input type="checkbox" v-model="scopes.putio" /> Remove from Put.io (cancel transfer + delete remote files)</label>
        <label><input type="checkbox" v-model="scopes.local" /> Delete local files under download directory</label>
        <label><input type="checkbox" v-model="scopes.db" /> Remove tracking row from database</label>
      </div>

      <div class="modal-actions">
        <button :disabled="busy" @click="emit('cancel')">Cancel</button>
        <button
          class="danger"
          :disabled="busy || (!scopes.putio && !scopes.local && !scopes.db)"
          @click="confirm"
        >
          {{ busy ? 'Deleting…' : 'Delete' }}
        </button>
      </div>
    </div>
  </div>
</template>
