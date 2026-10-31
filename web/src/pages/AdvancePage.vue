<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
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
  editAdvance,
  getAdvance,
  postAdvance,
  updateAdvance,
  voidAdvance,
  type Voucher,
  type VoucherDraft,
} from '@/api/advances'
import { formatDateTime, formatPesos } from '@/format'
import { useSessionStore } from '@/stores/session'
import { setPageTitle } from '@/pageTitle'

const route = useRoute()
const router = useRouter()
const { isManager } = storeToRefs(useSessionStore())

const id = computed(() => Number(route.params.id))

const voucher = ref<Voucher | null>(null)

const loading = ref(true)
const busy = ref(false)
const error = ref('')
const saved = ref('')

const voiding = ref(false)
const voidReason = ref('')

const isDraft = computed(() => voucher.value?.status === 'draft')

/** A posted slip opened for editing in place, in the same grid a draft uses. */
const editing = ref(false)

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
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loading.value = false
  }
}

// Every one of these reloads rather than trusting the response alone: posting
// and voiding both change the voucher's status and who it names.
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

// An edit is answered with a different voucher — the slip's new revision under
// the same Ref. No. — so the page moves to it rather than reloading this one.
async function saveEdit(draft: VoucherDraft): Promise<void> {
  error.value = ''
  saved.value = ''
  busy.value = true
  try {
    const edited = await editAdvance(id.value, draft)
    editing.value = false
    await router.replace({ name: 'advance', params: { id: edited.id } })
    saved.value = 'Saved. The slip keeps its Ref. No.'
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
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

// The same page component serves the next revision after an edit, so a change
// of id is a fresh load rather than a fresh mount.
watch(id, () => {
  editing.value = false
  void load()
})
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
      </PageHeader>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <Message v-else-if="saved" severity="success" :closable="false">{{ saved }}</Message>

      <!-- An earlier version of an edited slip: say so, and point at the current one. -->
      <Message v-if="voucher.superseded_by" severity="info" :closable="false">
        This is how the slip read before {{ voucher.voided_by.Username }} edited it.
        <RouterLink :to="{ name: 'advance', params: { id: voucher.superseded_by } }">
          See it as it reads now
        </RouterLink>
      </Message>
      <Message v-else-if="voucher.status === 'voided'" severity="warn" :closable="false">
        Voided by {{ voucher.voided_by.Username }} — {{ voucher.void_reason }}
      </Message>

      <!--
        A draft is the form. A posted slip is a record, opened in the same grid
        when somebody edits it; voiding it outright is a manager's.
      -->
      <VoucherLines
        v-if="editing"
        :date="voucher.date"
        :notes="voucher.notes"
        :lines="voucher.lines"
        :busy="busy"
        submit-label="Save changes"
        @submit="saveEdit"
        @cancel="editing = false"
      />

      <VoucherLines
        v-else-if="isDraft"
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
          Who posted it, said in a line. The ledger entries behind it are on the
          customer's statement; repeating them here was a second table nobody read.
        -->
        <p v-if="voucher.posted_at && voucher.revision > 1" class="advance__by">
          Edited by {{ voucher.created_by.Username }} on {{ formatDateTime(voucher.created_at) }}
        </p>
        <p v-else-if="voucher.posted_at" class="advance__by">
          Posted by {{ voucher.posted_by.Username }} on {{ formatDateTime(voucher.posted_at) }}
        </p>

        <div v-if="voucher.status === 'posted'" class="advance__actions">
          <Button
            label="Edit"
            icon="pi pi-pencil"
            severity="secondary"
            outlined
            :disabled="busy"
            @click="editing = true"
          />
          <Button
            v-if="isManager"
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

/* The table's own `th` rule is more specific than the class above; without
   this the heading sat left while its figures sat right. */
.advance__table th.advance__amount {
  text-align: right;
}

.advance__by {
  margin: 0.75rem 0 0;
  font-size: 0.875rem;
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
