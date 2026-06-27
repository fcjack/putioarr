<script setup lang="ts">
defineProps<{
  title: string
  message: string
  confirmLabel?: string
  busy?: boolean
}>()

const emit = defineEmits<{ (e: 'confirm'): void; (e: 'cancel'): void }>()
</script>

<template>
  <div class="modal-backdrop" @click.self="emit('cancel')">
    <div class="modal" role="dialog" aria-modal="true">
      <h3>{{ title }}</h3>
      <p class="muted">{{ message }}</p>
      <div class="modal-actions">
        <button :disabled="busy" @click="emit('cancel')">Cancel</button>
        <button class="danger" :disabled="busy" @click="emit('confirm')">
          {{ busy ? 'Working…' : (confirmLabel ?? 'Confirm') }}
        </button>
      </div>
    </div>
  </div>
</template>
