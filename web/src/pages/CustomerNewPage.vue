<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import CustomerFields from '@/components/CustomerFields.vue'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { createCustomer, type CustomerDraft } from '@/api/customers'

const router = useRouter()

const draft = ref<CustomerDraft>({ name: '', address: '', cell_no: '', notes: '', barangay_id: 0 })
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
  <div>
    <PageHeader
      title="New customer"
      description="The code is assigned when you save, from the barangay: the next one in its sequence."
    />

    <Message v-if="error" severity="error" :closable="false" class="new-customer__error">
      {{ error }}
    </Message>

    <form class="card card--padded new-customer" @submit.prevent="submit">
      <CustomerFields v-model="draft" autofocus is-new />

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
  </div>
</template>

<style scoped>
.new-customer__error {
  margin-bottom: 1rem;
}

.new-customer {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.new-customer__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
