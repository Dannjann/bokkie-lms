<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Message from 'primevue/message'
import PageHeader from '@/components/PageHeader.vue'
import PaymentFields from '@/components/PaymentFields.vue'
import { ApiError } from '@/api/client'
import { createAndPostPayment, createPayment, type Payment, type PaymentDraft } from '@/api/payments'
import { getCustomer } from '@/api/customers'
import { toISODate } from '@/format'

const route = useRoute()
const router = useRouter()

const error = ref('')
const busy = ref(false)

/**
 * A blank payment, or one already addressed to a farmer.
 *
 * The customer page's "Record payment" button arrives here with ?customer=, so
 * that the person who has just looked at a balance does not have to type the
 * name they are already looking at.
 */
const prefilled = ref<Payment | null>(null)

async function prefill(): Promise<void> {
  const id = Number(route.query.customer)
  if (!Number.isInteger(id) || id <= 0) return
  try {
    const detail = await getCustomer(id)
    prefilled.value = {
      customer_id: detail.customer.id,
      customer_code: detail.customer.code,
      customer_name: detail.customer.name,
      date: toISODate(new Date()),
      amount: '',
      method: 'cash',
      lines: [] as Payment['lines'],
      particular: '',
    } as Payment
  } catch {
    // Not worth an error on the screen: the form opens blank and the farmer is
    // typed in, which is what it would have been without the link.
  }
}

async function save(draft: PaymentDraft, andPost = false): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    const created = await (andPost ? createAndPostPayment(draft) : createPayment(draft))
    await router.replace({ name: 'payment', params: { id: created.id } })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

void prefill()
</script>

<template>
  <div>
    <PageHeader
      title="New payment"
      description="Record it now, or save a draft to finish later. Recording gives it an OR No.; it is applied to the balance when the farmer is settled."
    />

    <Message v-if="error" severity="error" :closable="false" class="payment-new__error">
      {{ error }}
    </Message>

    <div class="card card--padded">
      <PaymentFields
        :payment="prefilled"
        :busy="busy"
        submit-label="Save draft"
        post-label="Record payment"
        @submit="save"
        @post="save($event, true)"
        @cancel="router.back()"
      />
    </div>
  </div>
</template>

<style scoped>
.payment-new__error {
  margin-bottom: 1rem;
}
</style>
