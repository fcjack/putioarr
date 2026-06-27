<script setup lang="ts">
import { computed } from 'vue'
import { statusLabel, statusTone } from '@/utils'

const props = defineProps<{ status: string }>()

const label = computed(() => statusLabel(props.status))
const tone = computed(() => statusTone(props.status))
</script>

<template>
  <span class="badge" :class="tone">{{ label }}</span>
</template>
