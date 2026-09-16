<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Message from 'primevue/message'
import PageHeader from '@/components/PageHeader.vue'
import { listCustomers } from '@/api/customers'
import { useHealthStore } from '@/stores/health'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const health = useHealthStore()
const { health: server, error: serverError } = storeToRefs(health)
const { user, isManager } = storeToRefs(useSessionStore())

const active = ref<number | null>(null)
const inactive = ref<number | null>(null)
const countsFailed = ref(false)

/**
 * Two requests for two numbers, asking for a single row each.
 *
 * The count we want is the `total` the list endpoint already returns, so this
 * needs no new route on the server and no cached counter to go stale. On a LAN
 * it is two round trips of a few hundred bytes.
 */
async function loadCounts(): Promise<void> {
  countsFailed.value = false
  try {
    const [on, off] = await Promise.all([
      listCustomers({ status: 'active', perPage: 1 }),
      listCustomers({ status: 'inactive', perPage: 1 }),
    ])
    active.value = on.total
    inactive.value = off.total
  } catch {
    active.value = null
    inactive.value = null
    countsFailed.value = true
  }
}

const greeting = computed(() => {
  const hour = new Date().getHours()
  const part = hour < 12 ? 'Good morning' : hour < 18 ? 'Good afternoon' : 'Good evening'
  const name = user.value?.display_name.split(/\s+/)[0]
  return name ? `${part}, ${name}.` : `${part}.`
})

const uptime = computed(() => {
  const seconds = server.value?.uptime_seconds
  if (seconds === undefined) return '—'
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  const hours = Math.floor(seconds / 3600)
  return hours < 48 ? `${hours}h` : `${Math.floor(hours / 24)}d`
})

/** The nav's disabled entries, said once in prose so the order is visible. */
const upcoming = [
  { label: 'Advances', note: 'Voucher entry with lines, drafts, and voids by reversal.' },
  { label: 'Payments', note: 'Entry, allocation against advances, and a customer statement.' },
  { label: 'Interest', note: 'A reversible posting run — waiting on the owner interview.' },
  { label: 'Reports', note: 'Current Advances, ROI, Liquidated, aging, and A4 printing.' },
]

onMounted(loadCounts)
</script>

<template>
  <div>
    <PageHeader title="Dashboard" :description="greeting" />

    <Message v-if="countsFailed" severity="warn" :closable="false" class="dashboard__warning">
      Could not read the customer counts. The tiles below are blank, not zero.
    </Message>

    <section class="tiles" aria-label="Customers on the books">
      <article class="card card--padded tile">
        <p class="label">Active customers</p>
        <p class="tile__value numeric">{{ active ?? '—' }}</p>
        <p class="tile__note">Can be given an advance.</p>
      </article>

      <article class="card card--padded tile">
        <p class="label">Deactivated</p>
        <p class="tile__value numeric">{{ inactive ?? '—' }}</p>
        <p class="tile__note">Kept, never deleted — their codes stay theirs.</p>
      </article>

      <article class="card card--padded tile">
        <p class="label">Outstanding</p>
        <p class="tile__value numeric tile__value--muted">₱0.00</p>
        <p class="tile__note">Nothing can be owed until advances exist.</p>
      </article>
    </section>

    <div class="dashboard__columns">
      <section class="card card--padded">
        <h2 class="dashboard__heading">Start here</h2>
        <div class="dashboard__actions">
          <Button
            label="Find a customer"
            icon="pi pi-search"
            severity="secondary"
            outlined
            @click="router.push({ name: 'customers' })"
          />
          <Button
            label="New customer"
            icon="pi pi-plus"
            @click="router.push({ name: 'customer-new' })"
          />
          <Button
            v-if="isManager"
            label="Import CSV"
            icon="pi pi-upload"
            severity="secondary"
            outlined
            @click="router.push({ name: 'customers-import' })"
          />
        </div>

        <h2 class="dashboard__heading dashboard__heading--spaced">Coming next</h2>
        <ul class="upcoming">
          <li v-for="step in upcoming" :key="step.label" class="upcoming__item">
            <span class="upcoming__label">{{ step.label }}</span>
            <span class="upcoming__note">{{ step.note }}</span>
          </li>
        </ul>
      </section>

      <!--
        Not decoration. This is what someone reads out over the phone when a
        counter PC says it cannot reach the server, and what proves which build
        is actually running after a USB update.
      -->
      <section class="card card--padded">
        <h2 class="dashboard__heading">This server</h2>

        <p v-if="serverError" role="alert" class="dashboard__down">
          Cannot reach it: {{ serverError }}
        </p>

        <dl v-else-if="server" class="facts">
          <dt>Status</dt>
          <dd class="facts__value">{{ server.status }}</dd>
          <dt>Database</dt>
          <dd class="facts__value">{{ server.database }}</dd>
          <dt>Build</dt>
          <dd class="facts__value numeric">{{ server.version }} · {{ server.commit }}</dd>
          <dt>Built</dt>
          <dd class="facts__value numeric">{{ server.built }}</dd>
          <dt>Running for</dt>
          <dd class="facts__value numeric">{{ uptime }}</dd>
        </dl>

        <p v-else class="tile__note">Checking…</p>

        <Button
          label="Check again"
          icon="pi pi-refresh"
          severity="secondary"
          text
          size="small"
          class="dashboard__recheck"
          @click="health.load(); loadCounts()"
        />
      </section>
    </div>
  </div>
</template>

<style scoped>
.dashboard__warning {
  margin-bottom: 1rem;
}

.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr));
  gap: 1rem;
  margin-bottom: 1.25rem;
}

.tile__value {
  margin: 0.25rem 0 0;
  font-size: 1.875rem;
  font-weight: 600;
  line-height: 1.1;
  color: var(--p-surface-900);
}

.tile__value--muted {
  color: var(--p-surface-400);
}

.tile__note {
  margin: 0.35rem 0 0;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.dashboard__columns {
  display: grid;
  grid-template-columns: 1.35fr 1fr;
  gap: 1rem;
  align-items: start;
}

.dashboard__heading {
  margin: 0 0 0.75rem;
  font-size: 0.9375rem;
  font-weight: 600;
}

.dashboard__heading--spaced {
  margin-top: 1.5rem;
}

.dashboard__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.upcoming {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
}

.upcoming__item {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  padding: 0.55rem 0;
  border-top: 1px solid var(--shell-border);
}

.upcoming__item:first-child {
  border-top: none;
  padding-top: 0;
}

.upcoming__label {
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--p-surface-600);
}

.upcoming__note {
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.facts {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.4rem 1rem;
  margin: 0;
  font-size: 0.875rem;
}

.facts dt {
  color: var(--shell-muted);
}

.facts__value {
  margin: 0;
  word-break: break-word;
}

.dashboard__down {
  margin: 0;
  font-size: 0.875rem;
  color: #b91c1c;
}

.dashboard__recheck {
  margin-top: 0.75rem;
  margin-left: -0.5rem;
}

@media (max-width: 62rem) {
  .dashboard__columns {
    grid-template-columns: 1fr;
  }
}
</style>
