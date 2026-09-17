<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import Message from 'primevue/message'
import ToggleSwitch from 'primevue/toggleswitch'
import PageHeader from '@/components/PageHeader.vue'
import { listCustomers } from '@/api/customers'
import { lanAccessKey, listSettings, updateSetting } from '@/api/settings'
import { ApiError } from '@/api/client'
import { useHealthStore } from '@/stores/health'
import { useSessionStore } from '@/stores/session'

const router = useRouter()
const health = useHealthStore()
const { health: server, error: serverError } = storeToRefs(health)
const { user, isManager, isHost } = storeToRefs(useSessionStore())

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

/**
 * Whether the other machines on the station network may reach this app.
 *
 * Null while it is being read, and left null if it cannot be — a switch that
 * shows "off" because a request failed would be a lie about the one thing on
 * this screen that decides who can work.
 */
const lanAccess = ref<boolean | null>(null)
const lanBusy = ref(false)
const lanError = ref<string | null>(null)

/**
 * Turning access off is a two-step click. The effect is immediate and reaches
 * every other machine at once, so the person doing it should have said so on
 * purpose. Turning it back on needs no ceremony: it only ever gives access
 * back.
 */
const confirming = ref(false)

const lanNote = computed(() => {
  if (lanAccess.value === null) return 'Checking…'
  return lanAccess.value
    ? 'Counter PCs and phones on the station network can reach this app.'
    : 'Only this machine can reach the app. Everything else is refused.'
})

async function loadLanAccess(): Promise<void> {
  if (!isManager.value) return
  lanError.value = null
  try {
    const { settings } = await listSettings()
    lanAccess.value = settings.find((s) => s.key === lanAccessKey)?.value === 'true'
  } catch {
    lanAccess.value = null
    lanError.value = 'Could not read the network setting.'
  }
}

function onToggle(next: boolean): void {
  if (next) {
    void setLanAccess(true)
    return
  }
  confirming.value = true
}

async function setLanAccess(value: boolean): Promise<void> {
  lanBusy.value = true
  lanError.value = null
  try {
    const setting = await updateSetting(lanAccessKey, value ? 'true' : 'false')
    lanAccess.value = setting.value === 'true'
    confirming.value = false
  } catch (err) {
    lanError.value = err instanceof ApiError ? err.message : String(err)
  } finally {
    lanBusy.value = false
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
  { label: 'Reports', note: 'Current Advances, ROI, Liquidated, aging, and A4 printing.' },
  { label: 'Statements', note: 'Across customers, with the print layout that makes one worth handing over.' },
  { label: 'Slips', note: 'Vouchers and receipts on the dot-matrix, on continuous form.' },
]

onMounted(() => {
  void loadCounts()
  void loadLanAccess()
})
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

        <!--
          Who is allowed in. It lives on the dashboard rather than behind a
          settings screen because it is switched for a reason that turns up
          without warning — a visitor on the WiFi, a machine nobody trusts
          today — and hunting for it is not what that moment needs.
        -->
        <section v-if="isManager" class="network" aria-labelledby="network-heading">
          <div class="network__row">
            <div>
              <p id="network-heading" class="label">Network access</p>
              <p class="tile__note">{{ lanNote }}</p>
            </div>
            <ToggleSwitch
              :model-value="lanAccess === true"
              :disabled="lanAccess === null || lanBusy || !isHost"
              aria-labelledby="network-heading"
              @update:model-value="onToggle"
            />
          </div>

          <p v-if="!isHost" class="tile__note network__note">
            This can only be changed at the host machine — the one bokkie is installed on.
          </p>

          <div v-if="confirming" class="network__confirm">
            <p class="network__confirm-text">
              Turn network access off? Every other machine loses the app immediately. This
              one keeps it, and this switch turns it back on.
            </p>
            <div class="network__confirm-actions">
              <Button
                label="Cancel"
                severity="secondary"
                text
                size="small"
                :disabled="lanBusy"
                @click="confirming = false"
              />
              <Button
                label="Turn off"
                severity="danger"
                size="small"
                :loading="lanBusy"
                @click="setLanAccess(false)"
              />
            </div>
          </div>

          <p v-if="lanError" role="alert" class="dashboard__down network__note">{{ lanError }}</p>
        </section>

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

.network {
  margin-top: 1rem;
  padding-top: 0.9rem;
  border-top: 1px solid var(--shell-border);
}

.network__row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
}

.network__note {
  margin-top: 0.5rem;
}

.network__confirm {
  margin-top: 0.75rem;
  padding: 0.75rem;
  border: 1px solid var(--shell-border);
  border-radius: 6px;
  background: var(--p-surface-50);
}

.network__confirm-text {
  margin: 0 0 0.6rem;
  font-size: 0.8125rem;
  line-height: 1.5;
}

.network__confirm-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.4rem;
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
