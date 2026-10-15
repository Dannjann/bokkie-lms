<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Message from 'primevue/message'
import ProgressSpinner from 'primevue/progressspinner'
import Tag from 'primevue/tag'
import Textarea from 'primevue/textarea'
import PageHeader from '@/components/PageHeader.vue'
import VoucherLines from '@/components/VoucherLines.vue'
import { ApiError } from '@/api/client'
import {
  deleteAdvance,
  getAdvance,
  postAdvance,
  updateAdvance,
  voidAdvance,
  type LedgerEntryView,
  type Voucher,
  type VoucherDraft,
} from '@/api/advances'
import { formatPesos } from '@/format'
import { useSessionStore } from '@/stores/session'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()
const { isManager } = storeToRefs(useSessionStore())

const id = computed(() => Number(route.params.id))

const voucher = ref<Voucher | null>(null)
const entries = ref<LedgerEntryView[]>([])

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const saved = ref('')

const voiding = ref(false)
const voidReason = ref('')

const isDraft = computed(() => voucher.value?.status === 'draft')

function adopt(loaded: Voucher): void {
  voucher.value = loaded
  setPageTitle(loaded.ref_no ? `Ref. No. ${loaded.ref_no}` : 'Draft advance slip')
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const detail = await getAdvance(id.value)
    adopt(detail.voucher)
    entries.value = detail.entries
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

// Every one of these reloads rather than trusting the response alone: posting
// and voiding both change what the ledger says about the voucher, and the
// entries panel underneath is the point of looking at a posted one.
async function run(what: string, action: () => Promise<Voucher>): Promise<void> {
  error.value = ''
  saved.value = ''
  busy.value = true
  try {
    adopt(await action())
    await load()
    saved.value = what
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

function save(draft: VoucherDraft): void {
  void run('Saved.', () => updateAdvance(id.value, draft))
}

function post(): void {
  void run('Posted.', () => postAdvance(id.value))
}

function confirmVoid(): void {
  voiding.value = false
  void run('Voided.', () => voidAdvance(id.value, voidReason.value))
}

async function discard(): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await deleteAdvance(id.value)
    await router.replace({ name: 'advances' })
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="advance">
    <div v-if="loading" class="advance__loading">
      <ProgressSpinner style="width: 2.5rem; height: 2.5rem" />
    </div>

    <template v-else-if="voucher">
      <PageHeader>
        <template #title>
          <p class="advance__ref numeric label">
            {{ voucher.ref_no ? `Ref. No. ${voucher.ref_no}` : 'Unnumbered draft' }}
          </p>
          <h1 class="advance__heading">
            {{ formatPesos(voucher.total) }}
            <Tag v-if="voucher.status === 'draft'" severity="warn" value="Draft" />
            <Tag v-else-if="voucher.status === 'voided'" severity="danger" value="Voided" />
          </h1>
        </template>
        <template #actions>
          <Button
            label="Back to advances"
            icon="pi pi-arrow-left"
            severity="secondary"
            text
            @click="router.push({ name: 'advances' })"
          />
        </template>
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">{{ saved }}</Message>

      <Message v-if="voucher.status === 'voided'" severity="warn" :closable="false">
        Voided by {{ voucher.voided_by.Username }} — {{ voucher.void_reason }}
      </Message>

      <!--
        A draft is the form. Everything else is a record of what happened, and
        the only thing that can be done to it is a void, which is a manager's.
      -->
      <VoucherLines
        v-if="isDraft"
        :date="voucher.date"
        :notes="voucher.notes"
        :lines="voucher.lines"
        :busy="busy"
        @submit="save"
        @cancel="router.push({ name: 'advances' })"
      >
        <template #actions>
          <Button
            label="Delete draft"
            icon="pi pi-trash"
            severity="danger"
            text
            :disabled="busy"
            @click="discard"
          />
          <Button
            label="Post"
            icon="pi pi-check"
            severity="success"
            :disabled="busy"
            @click="post"
          />
        </template>
      </VoucherLines>

      <template v-else>
        <div class="card advance__grid">
          <table class="advance__table">
            <thead>
              <tr>
                <th>Customer</th>
                <th class="advance__date">Date</th>
                <th>Particular</th>
                <th class="advance__amount">Amount</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="line in voucher.lines" :key="line.id">
                <td>
                  <RouterLink :to="{ name: 'customer', params: { id: line.customer_id } }">
                    {{ line.customer_code }} — {{ line.customer_name }}
                  </RouterLink>
                </td>
                <td class="advance__date numeric">{{ line.date }}</td>
                <td>{{ line.particular }}</td>
                <td class="advance__amount numeric">{{ formatPesos(line.amount) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td colspan="3">{{ voucher.lines.length }} lines</td>
                <td class="advance__amount numeric">{{ formatPesos(voucher.total) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>

        <!--
          Not a restatement of the lines: what the ledger actually holds because
          of this voucher. After a void the reversals appear here, which is the
          whole of the evidence that the money went back.
        -->
        <section class="card advance__grid">
          <h2 class="advance__section">In the ledger</h2>
          <table class="advance__table">
            <thead>
              <tr>
                <th class="advance__date">Date</th>
                <th>Entry</th>
                <th>By</th>
                <th class="advance__amount">Principal</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in entries" :key="entry.id">
                <td class="advance__date numeric">{{ entry.date }}</td>
                <td>
                  {{ entry.type === 'reversal' ? 'Reversal' : 'Advance' }}
                  <span v-if="entry.reason" class="advance__reason">— {{ entry.reason }}</span>
                </td>
                <td>{{ entry.by }}</td>
                <td class="advance__amount numeric">{{ formatPesos(entry.principal) }}</td>
              </tr>
            </tbody>
          </table>
        </section>

        <div v-if="isManager && voucher.status === 'posted'" class="advance__actions">
          <Button
            label="Void this slip"
            icon="pi pi-ban"
            severity="danger"
            outlined
            :disabled="busy"
            @click="voiding = true"
          />
        </div>
      </template>
    </template>

    <Message v-else severity="error" :closable="false">{{ error || 'No such advance slip.' }}</Message>

    <Dialog v-model:visible="voiding" modal header="Void this slip" :style="{ width: '28rem' }">
      <p class="advance__dialog-text">
        Every line will be reversed in the ledger. The slip and its reversals both stay on the
        record, and the reason is written onto each one.
      </p>
      <Textarea v-model="voidReason" rows="3" fluid aria-label="Reason for voiding" />
      <template #footer>
        <Button label="Cancel" severity="secondary" text @click="voiding = false" />
        <Button
          label="Void"
          severity="danger"
          :disabled="voidReason.trim() === ''"
          @click="confirmVoid"
        />
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
.advance {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.advance__loading {
  display: grid;
  place-items: center;
  padding: 4rem 0;
}

.advance__ref {
  margin: 0;
}

.advance__heading {
  margin: 0.15rem 0 0;
  font-size: 1.375rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  font-variant-numeric: tabular-nums;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.advance__grid {
  /* min-width: 0 for the same reason as the entry grid's: a flex item will not
     shrink below its content without it, and overflow-x then contains nothing. */
  min-width: 0;
  overflow-x: auto;
  padding: 0.25rem 0.75rem 0.75rem;
}

.advance__section {
  margin: 0.75rem 0.25rem 0.5rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.advance__table {
  width: 100%;
  min-width: 34rem;
  border-collapse: collapse;
}

.advance__table th {
  padding: 0.6rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.advance__table td {
  padding: 0.5rem;
  border-bottom: 1px solid var(--shell-border);
}

.advance__table tfoot td {
  border-bottom: none;
  font-weight: 600;
}

.advance__date {
  white-space: nowrap;
}

.advance__amount {
  text-align: right;
  white-space: nowrap;
}

.advance__reason {
  color: var(--shell-muted);
}

.advance__actions {
  display: flex;
  justify-content: flex-end;
}

.advance__dialog-text {
  margin: 0 0 0.75rem;
  color: var(--shell-muted);
}
</style>
