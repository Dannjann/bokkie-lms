<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Select from 'primevue/select'
import DatePicker from 'primevue/datepicker'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  auditFacets,
  describeAction,
  isAlarming,
  listAudit,
  prettyJSON,
  type AuditEntry,
  type AuditFacets,
} from '@/api/audit'
import { toISODate } from '@/format'

/**
 * The audit log.
 *
 * The only screen in the system with no write path at all, and the only one
 * whose value is entirely in being read months after the fact. Everything here
 * serves one question — "what happened, and who did it" — which is why the
 * filter bar is the first thing on the page rather than a panel somebody has to
 * find.
 */

const entries = ref<AuditEntry[]>([])
const facets = ref<AuditFacets>({ users: [], actions: [], entities: [] })
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const next = ref(0)
const expanded = ref<AuditEntry[]>([])

const userId = ref<number | null>(null)
const action = ref<string | null>(null)
const entity = ref<string | null>(null)
const from = ref<Date | null>(null)
const to = ref<Date | null>(null)

const latest = new Date()

const userOptions = computed(() => [
  { label: 'Anyone', value: null },
  ...facets.value.users.map((u) => ({ label: u.username, value: u.id })),
])

const actionOptions = computed(() => [
  { label: 'Any action', value: null },
  ...facets.value.actions.map((a) => ({ label: describeAction(a), value: a })),
])

const entityOptions = computed(() => [
  { label: 'Any record', value: null },
  ...facets.value.entities.map((e) => ({ label: e, value: e })),
])

const filtered = computed(
  () => userId.value !== null || action.value !== null || entity.value !== null || from.value !== null || to.value !== null,
)

function query(before?: number) {
  return {
    userId: userId.value,
    action: action.value,
    entity: entity.value,
    from: from.value ? toISODate(from.value) : null,
    to: to.value ? toISODate(to.value) : null,
    before,
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  expanded.value = []
  try {
    const page = await listAudit(query())
    entries.value = page.entries
    next.value = page.next
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    entries.value = []
    next.value = 0
  } finally {
    loading.value = false
  }
}

/**
 * Append the next page.
 *
 * The cursor is the id of the oldest entry already shown, not a page number:
 * entries are only ever appended, so "everything older than the last one I saw"
 * cannot repeat or skip a row while new ones arrive during the scroll.
 */
async function loadMore(): Promise<void> {
  if (!next.value) return
  loadingMore.value = true
  error.value = ''
  try {
    const page = await listAudit(query(next.value))
    entries.value = [...entries.value, ...page.entries]
    next.value = page.next
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    loadingMore.value = false
  }
}

function clearFilters(): void {
  userId.value = null
  action.value = null
  entity.value = null
  from.value = null
  to.value = null
}

// Any filter change reloads from the top: a cursor taken under one filter means
// nothing under another.
watch([userId, action, entity, from, to], load)

/**
 * "18 Sep 2026, 08:14:02" — the server has already put it in station time, so
 * nothing here converts anything; it only chooses a shape.
 *
 * Seconds are kept. This is the one screen where two entries a second apart is
 * the distinction somebody is looking for: an access denial and the sign-in
 * that preceded it, or a post and the void that undid it.
 */
function when(at: string): string {
  const date = new Date(at)
  if (Number.isNaN(date.getTime())) return at
  return date.toLocaleString('en-PH', { dateStyle: 'medium', timeStyle: 'medium' })
}

/** The same instant with the year and the month name dropped, for narrow screens. */
function whenShort(at: string): string {
  const date = new Date(at)
  if (Number.isNaN(date.getTime())) return at
  return date.toLocaleString('en-PH', {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

onMounted(async () => {
  await Promise.all([
    load(),
    auditFacets()
      .then((f) => {
        facets.value = f
      })
      // A failed facets call leaves the dropdowns holding only their "any"
      // option. The log itself still loads, which is the part that matters.
      .catch(() => {}),
  ])
})
</script>

<template>
  <div>
    <PageHeader
      title="Audit log"
      description="Everything that has changed, who changed it and when. Entries are never edited or deleted — the database itself refuses."
    />

    <Message v-if="error" severity="error" :closable="false" class="audit__banner">
      {{ error }}
    </Message>

    <div class="card card--padded audit__filters">
      <div class="audit__field">
        <label for="audit-user" class="label">Who</label>
        <Select
          input-id="audit-user"
          v-model="userId"
          :options="userOptions"
          option-label="label"
          option-value="value"
          placeholder="Anyone"
          class="audit__select"
        />
      </div>
      <div class="audit__field">
        <label for="audit-action" class="label">Action</label>
        <Select
          input-id="audit-action"
          v-model="action"
          :options="actionOptions"
          option-label="label"
          option-value="value"
          placeholder="Any action"
          filter
          class="audit__select audit__select--wide"
        />
      </div>
      <div class="audit__field">
        <label for="audit-entity" class="label">Record</label>
        <Select
          input-id="audit-entity"
          v-model="entity"
          :options="entityOptions"
          option-label="label"
          option-value="value"
          placeholder="Any record"
          class="audit__select"
        />
      </div>
      <div class="audit__field">
        <label for="audit-from" class="label">From</label>
        <DatePicker
          input-id="audit-from"
          v-model="from"
          date-format="yy-mm-dd"
          :max-date="latest"
          show-icon
          icon-display="input"
          show-button-bar
        />
      </div>
      <div class="audit__field">
        <label for="audit-to" class="label">To</label>
        <DatePicker
          input-id="audit-to"
          v-model="to"
          date-format="yy-mm-dd"
          :max-date="latest"
          show-icon
          icon-display="input"
          show-button-bar
        />
      </div>
      <Button
        v-if="filtered"
        label="Clear"
        icon="pi pi-times"
        severity="secondary"
        text
        class="audit__clear"
        @click="clearFilters"
      />
    </div>

    <div class="card">
      <DataTable
        v-model:expanded-rows="expanded"
        :value="entries"
        :loading="loading"
        data-key="id"
        class="audit__table"
      >
        <template #empty>
          <p class="audit__empty">
            {{ filtered ? 'Nothing matches those filters.' : 'Nothing has happened yet.' }}
          </p>
        </template>

        <Column header="When" class="audit__when">
          <template #body="{ data }">
            <!-- Two renderings, one shown at a time by CSS. A resize listener
                 would be the other way to do this and would re-render the whole
                 table on every drag of a window edge. -->
            <span class="audit__when-full">{{ when(data.at) }}</span>
            <span class="audit__when-short">{{ whenShort(data.at) }}</span>
          </template>
        </Column>

        <Column header="Who" class="audit__who">
          <template #body="{ data }">{{ data.username || '—' }}</template>
        </Column>

        <Column header="Action" class="audit__action">
          <template #body="{ data }">
            <Tag
              v-if="isAlarming(data.action)"
              severity="danger"
              :value="describeAction(data.action)"
            />
            <span v-else>{{ describeAction(data.action) }}</span>
          </template>
        </Column>

        <Column header="Record" class="audit__record">
          <template #body="{ data }">
            <span v-if="data.entity_id" class="audit__entity">
              <span class="audit__entity-kind">{{ data.entity }}</span>
              {{ data.entity_id }}
            </span>
            <span v-else class="audit__none">—</span>
          </template>
        </Column>

        <!-- The expander is a column of the table, so the detail opens as a row
             directly under its entry. Not a dialog: reconstructing an incident
             means reading several of these against each other, and a dialog
             shows one at a time. -->
        <Column expander class="audit__expand" />

        <template #expansion="{ data }">
          <div class="audit__detail">
            <div class="audit__diff">
              <div v-if="data.before" class="audit__side">
                <h4>Before</h4>
                <pre>{{ prettyJSON(data.before) }}</pre>
              </div>
              <div v-if="data.after" class="audit__side">
                <h4>After</h4>
                <pre>{{ prettyJSON(data.after) }}</pre>
              </div>
              <p v-if="!data.before && !data.after" class="audit__none">
                Nothing was recorded for this entry beyond what is in the row.
              </p>
            </div>
            <p class="audit__meta">
              <span v-if="data.request_id">Request {{ data.request_id }}</span>
              <span v-if="data.ip">IP {{ data.ip }}</span>
            </p>
          </div>
        </template>
      </DataTable>

      <div v-if="next" class="audit__more">
        <Button
          label="Load older"
          icon="pi pi-arrow-down"
          severity="secondary"
          outlined
          :loading="loadingMore"
          @click="loadMore"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.audit__banner {
  margin-bottom: 1rem;
}

.audit__filters {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 0.75rem;
  margin-bottom: 1rem;
}

.audit__field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.audit__select {
  min-width: 10rem;
}

.audit__select--wide {
  min-width: 14rem;
}

.audit__clear {
  margin-left: auto;
}

.audit__table :deep(.audit__when) {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  width: 1%;
}

.audit__table :deep(.audit__who) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
  white-space: nowrap;
  width: 1%;
}

.audit__table :deep(.audit__action),
.audit__table :deep(.audit__record) {
  white-space: nowrap;
}

.audit__table :deep(.audit__expand) {
  width: 1%;
}

.audit__entity-kind {
  color: var(--shell-muted);
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.03em;
  margin-right: 0.35rem;
}

.audit__none {
  color: var(--p-surface-400);
}

.audit__table :deep(.p-datatable-table-container) {
  border-radius: 0;
  /* Whatever else narrows, the expander must stay reachable: the table scrolls
     inside the card rather than clipping columns off its right edge. That is
     the fault the Users screen had at 1024px, where a page-overflow check sees
     nothing because the document itself never gets wider. */
  overflow-x: auto;
}

.audit__when-short {
  display: none;
}

.audit__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

.audit__detail {
  padding: 0.85rem 1rem;
  border-top: 1px solid var(--shell-border);
  background: var(--p-surface-50);
}

.audit__diff {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
}

.audit__side {
  flex: 1 1 16rem;
  min-width: 0;
}

.audit__side h4 {
  margin: 0 0 0.3rem;
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--shell-muted);
}

/* The stored JSON can be long and is not wrapped: it scrolls inside its own
   box rather than dragging the page wide, which is the fault step 5 found. */
.audit__side pre {
  margin: 0;
  padding: 0.6rem;
  max-height: 16rem;
  overflow: auto;
  border: 1px solid var(--shell-border);
  border-radius: 6px;
  background: var(--shell-surface);
  font-size: 0.75rem;
  line-height: 1.45;
}

.audit__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 1rem;
  margin: 0.6rem 0 0;
  font-size: 0.75rem;
  color: var(--shell-muted);
}

.audit__more {
  display: flex;
  justify-content: center;
  padding: 0.85rem;
  border-top: 1px solid var(--shell-border);
}

@media (max-width: 60rem) {
  /* The flex property goes on the field, never on the Select inside it. The
     field is a column flex container, so a flex-grow on its child stretches
     the control *vertically* — which turned each dropdown into a 150px box at
     390px, and is invisible at any width where the bar does not wrap. */
  .audit__field {
    min-width: 0;
    flex: 1 1 10rem;
  }

  .audit__select,
  .audit__select--wide {
    min-width: 0;
    width: 100%;
  }

  .audit__clear {
    margin-left: 0;
  }

  .audit__table :deep(.audit__who) {
    display: none;
  }
}

/* Below the counter PC's width the full date is the widest thing in the row
   and the least informative part of it — the year is always this year. */
@media (max-width: 45rem) {
  .audit__when-full {
    display: none;
  }

  .audit__when-short {
    display: inline;
  }
}
</style>
