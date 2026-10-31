<script setup lang="ts">
/**
 * A customer's ID card, ready to print.
 *
 * Bank-card size — 85.6 × 54 mm, what a card sleeve or a laminating pouch is
 * cut for — and printed at that size, not scaled to the page. The QR code
 * holds the customer code and nothing else: it is read back into the search
 * box, and the code is the one thing about a farmer that never changes.
 *
 * The photo is a box to paste one into. The system keeps no photos, and a
 * card printed without one is still a card.
 */
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import qrcode from 'qrcode-generator'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import { getCustomer, type Customer } from '@/api/customers'
import { formatPhone } from '@/format'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()

const customer = ref<Customer | null>(null)
const loading = ref(true)
const error = ref('')

/**
 * The QR code as an SVG string. Drawn here rather than fetched: the station's
 * network has no internet to fetch one from.
 */
const qr = computed(() => {
  if (!customer.value) return ''
  const code = qrcode(0, 'M')
  code.addData(customer.value.code)
  code.make()
  return code.createSvgTag({ cellSize: 4, margin: 0, scalable: true })
})

const since = computed(() =>
  customer.value
    ? new Date(customer.value.created_at).toLocaleDateString('en-PH', { year: 'numeric', month: 'short' })
    : '',
)

function print(): void {
  window.print()
}

onMounted(async () => {
  try {
    const detail = await getCustomer(Number(route.params.id))
    customer.value = detail.customer
    setPageTitle(`ID card — ${detail.customer.name}`)
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="id-page">
    <div v-if="loading" class="id-page__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <Message v-else-if="error || !customer" severity="error" :closable="false">
      {{ error || 'No such customer.' }}
    </Message>

    <template v-else>
      <PageHeader
        title="ID card"
        description="Prints at bank-card size. Paste a photo into the box, then laminate it."
      >
        <template #actions>
          <Button label="Back" severity="secondary" text @click="router.back()" />
          <Button label="Print" icon="pi pi-print" @click="print" />
        </template>
      </PageHeader>

      <article class="id-card" :aria-label="`ID card for ${customer.name}`">
        <header class="id-card__head">TLS Palay &amp; Corn Buying Station</header>

        <div class="id-card__body">
          <div class="id-card__photo" aria-label="Photo">Photo</div>

          <dl class="id-card__details">
            <dt class="sr-only">Name</dt>
            <dd class="id-card__name">{{ customer.name }}</dd>

            <dt class="sr-only">Code</dt>
            <dd class="id-card__code numeric">{{ customer.code }}</dd>

            <template v-if="customer.barangay_name">
              <dt>Barangay</dt>
              <dd>{{ customer.barangay_name }}</dd>
            </template>
            <template v-if="customer.address">
              <dt>Address</dt>
              <dd>{{ customer.address }}</dd>
            </template>
            <template v-if="customer.cell_no">
              <dt>Phone</dt>
              <dd class="numeric">{{ formatPhone(customer.cell_no) }}</dd>
            </template>
          </dl>

          <!-- v-html is safe here: the SVG is drawn from the customer code by the QR library. -->
          <div class="id-card__qr" role="img" :aria-label="`QR code for ${customer.code}`" v-html="qr" />
        </div>

        <footer class="id-card__foot">
          <span>Customer since {{ since }}</span>
          <span class="id-card__sign">Signature</span>
        </footer>
      </article>
    </template>
  </div>
</template>

<style scoped>
.id-page__loading {
  display: flex;
  justify-content: center;
  padding: 3rem;
}

/*
 * Millimetres, so the screen shows the card in its true proportions and the
 * printer puts it on paper at exactly the size of a bank card.
 */
.id-card {
  box-sizing: border-box;
  width: 85.6mm;
  height: 54mm;
  margin: 1rem 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 0.3mm solid #1f2937;
  border-radius: 3mm;
  background: #fff;
  color: #111827;
  font-size: 2.6mm;
  line-height: 1.25;
}

.id-card__head {
  padding: 1.6mm 3mm;
  background: #166534;
  color: #fff;
  font-size: 2.8mm;
  font-weight: 700;
  letter-spacing: 0.02em;
  print-color-adjust: exact;
  -webkit-print-color-adjust: exact;
}

.id-card__body {
  flex: 1;
  display: flex;
  gap: 2.5mm;
  padding: 2.5mm 3mm 1mm;
  min-height: 0;
}

.id-card__photo {
  flex: 0 0 20mm;
  height: 25mm;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 0.3mm dashed #9ca3af;
  color: #9ca3af;
  font-size: 2.4mm;
}

.id-card__details {
  flex: 1;
  min-width: 0;
  margin: 0;
  display: grid;
  grid-template-columns: auto 1fr;
  column-gap: 1.5mm;
  row-gap: 0.4mm;
  align-content: start;
}

.id-card__details dt {
  color: #6b7280;
  font-size: 2.2mm;
}

.id-card__details dd {
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.id-card__name,
.id-card__code {
  grid-column: 1 / -1;
}

.id-card__name {
  font-size: 3.4mm;
  font-weight: 700;
}

.id-card__code {
  margin-bottom: 1mm !important;
  font-size: 3.2mm;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.id-card__qr {
  flex: 0 0 18mm;
  height: 18mm;
  align-self: start;
}

.id-card__qr :deep(svg) {
  width: 100%;
  height: 100%;
}

.id-card__foot {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  padding: 0 3mm 1.8mm;
  color: #6b7280;
  font-size: 2mm;
}

.id-card__sign {
  width: 26mm;
  padding-top: 0.6mm;
  border-top: 0.2mm solid #9ca3af;
  text-align: center;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}

/* On paper: the card alone, at its true size, at the top left of the page. */
@media print {
  .id-card {
    margin: 0;
    break-inside: avoid;
  }
}
</style>
