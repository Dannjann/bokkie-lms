<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import CustomerFields from '@/components/CustomerFields.vue'
import { ApiError } from '@/api/client'
import { createCustomer, type CustomerDraft } from '@/api/customers'

const router = useRouter()

const draft = ref<CustomerDraft>({ name: '', address: '', cell_no: '', notes: '' })
const error = ref('')
const busy = ref(false)

async function submit(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    const created = await createCustomer(draft.value)
    await router.replace({ name: 'customer', params: { id: created.id } })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="new-customer" @submit.prevent="submit">
    <h1 class="new-customer__title">New customer</h1>
    <p class="new-customer__code">
      The code is assigned when you save, continuing from the highest one on the books.
    </p>

    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

    <CustomerFields v-model="draft" autofocus />

    <div class="new-customer__actions">
      <Button
        label="Cancel"
        severity="secondary"
        text
        :disabled="busy"
        @click="router.back()"
      />
      <Button type="submit" label="Save" :loading="busy" />
    </div>
  </form>
</template>

<style scoped>
.new-customer {
  max-width: 32rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.new-customer__title {
  margin: 0;
  font-size: 1.5rem;
}

.new-customer__code {
  margin: 0;
  color: var(--p-surface-500);
  font-size: 0.875rem;
}

.new-customer__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
