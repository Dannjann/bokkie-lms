<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import Message from 'primevue/message'
import PageHeader from '@/components/PageHeader.vue'
import VoucherLines from '@/components/VoucherLines.vue'
import { ApiError } from '@/api/client'
import { createAdvance, createAndPostAdvance, type VoucherDraft } from '@/api/advances'
import { toISODate } from '@/format'

const router = useRouter()

const error = ref('')
const busy = ref(false)
const today = toISODate(new Date())

async function save(draft: VoucherDraft, andPost = false): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    const created = await (andPost ? createAndPostAdvance(draft) : createAdvance(draft))
    await router.replace({ name: 'advance', params: { id: created.id } })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div>
    <PageHeader
      title="New advance slip"
      description="Post it now, or save a draft to finish later. A draft moves no money and gets no Ref. No. until it is posted."
    />

    <Message v-if="error" severity="error" :closable="false" class="advance-new__error">
      {{ error }}
    </Message>

    <VoucherLines
      :date="today"
      notes=""
      :lines="[]"
      :busy="busy"
      post-label="Post"
      @submit="save"
      @post="save($event, true)"
      @cancel="router.back()"
    />
  </div>
</template>

<style scoped>
.advance-new__error {
  margin-bottom: 1rem;
}
</style>
