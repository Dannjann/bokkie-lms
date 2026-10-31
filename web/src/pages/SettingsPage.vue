<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import Button from 'primevue/button'
import InputNumber from 'primevue/inputnumber'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import BarangaySettings from '@/components/BarangaySettings.vue'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  lanAccessKey,
  listSettings,
  settingChoiceLabels,
  settingGroups,
  settingLabels,
  timezones,
  updateSetting,
  type Setting,
} from '@/api/settings'
import { backupNow, formatBytes, listBackups, type BackupFile } from '@/api/backups'
import { useSessionStore } from '@/stores/session'
import { ratePercent } from '@/format'

/**
 * Everything that changes without a rebuild, and the backup folder underneath.
 *
 * The screen is generic: it renders whatever the server says exists, choosing a
 * control from `kind_slug`. The words around each control are not generic and
 * live in api/settings.ts, where a Go test checks them against settings.All —
 * a setting added on the server with no label here would otherwise render as
 * its own key.
 */

const { isHost } = storeToRefs(useSessionStore())

const settings = ref<Setting[]>([])
const loading = ref(false)
const error = ref('')

/** Per-key state, so one failing field cannot blank the others. */
const saving = ref<Record<string, boolean>>({})
const saved = ref<Record<string, boolean>>({})
const fieldError = ref<Record<string, string>>({})

const byKey = computed(() => {
  const map: Record<string, Setting> = {}
  for (const s of settings.value) map[s.key] = s
  return map
})

/**
 * The groups, plus a trailing one for anything the server has that this screen
 * was not told about.
 *
 * A setting must never be invisible. The vocabulary test makes an ungrouped
 * setting a build failure, but a build that has already shipped must still show
 * it rather than silently drop it.
 */
const groups = computed(() => {
  const placed = new Set<string>()
  const out = settingGroups.map((group) => {
    const items = group.keys.map((k) => byKey.value[k]).filter((s): s is Setting => !!s)
    items.forEach((s) => placed.add(s.key))
    return { ...group, items }
  })

  const rest = settings.value.filter((s) => !placed.has(s.key))
  if (rest.length > 0) {
    out.push({ label: 'Other', blurb: undefined, keys: [], items: rest })
  }
  return out.filter((g) => g.items.length > 0)
})

function labelFor(setting: Setting): string {
  return settingLabels[setting.key] ?? setting.key
}

function choiceOptions(setting: Setting) {
  return (setting.options ?? []).map((value) => ({
    label: settingChoiceLabels[value] ?? value,
    value,
  }))
}

/**
 * Whether this setting can be changed from this browser.
 *
 * Only one is ever refused: the server will not take network.lan_access from
 * anywhere but the host machine, because switching it off from a counter PC
 * takes away the app and the switch in one click. Rendering it live and letting
 * the server say no would be a control that looks broken.
 */
function blockedReason(setting: Setting): string {
  if (setting.key === lanAccessKey && !isHost.value) {
    return 'This can only be changed at the host machine — the one bokkie is installed on.'
  }
  return ''
}

/** Basis points are how this is stored and not how anyone thinks. */
function asPercent(value: string): string {
  if (!/^\d+$/.test(value.trim())) return ''
  return `${ratePercent(Number(value))} a month`
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    settings.value = (await listSettings()).settings
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    settings.value = []
  } finally {
    loading.value = false
  }
}

/**
 * Write one setting.
 *
 * The server's answer replaces the row rather than the typed value being kept:
 * it is the authority on what is now stored, and a refused change must leave
 * the field showing what the system actually holds rather than what somebody
 * wished it held.
 */
async function save(setting: Setting, raw: string): Promise<void> {
  if (raw === setting.value) return

  saving.value = { ...saving.value, [setting.key]: true }
  fieldError.value = { ...fieldError.value, [setting.key]: '' }
  saved.value = { ...saved.value, [setting.key]: false }

  try {
    const updated = await updateSetting(setting.key, raw)
    settings.value = settings.value.map((s) => (s.key === updated.key ? updated : s))
    saved.value = { ...saved.value, [setting.key]: true }
    setTimeout(() => {
      saved.value = { ...saved.value, [setting.key]: false }
    }, 2500)
  } catch (err) {
    fieldError.value = {
      ...fieldError.value,
      [setting.key]: err instanceof ApiError ? err.message : 'Cannot reach the server.',
    }
    // Force the control back to the stored value. Replacing the array is what
    // makes the bound input re-read it.
    settings.value = [...settings.value]
  } finally {
    saving.value = { ...saving.value, [setting.key]: false }
  }
}

// --- Backups ---------------------------------------------------------------

const backups = ref<BackupFile[]>([])
const backupFolder = ref('')
const backupError = ref('')
const backupNotice = ref('')
const backingUp = ref(false)

async function loadBackups(): Promise<void> {
  try {
    const result = await listBackups()
    backups.value = result.backups
    backupFolder.value = result.folder
    backupError.value = ''
  } catch (err) {
    backupError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    backups.value = []
  }
}

async function runBackup(): Promise<void> {
  backingUp.value = true
  backupError.value = ''
  backupNotice.value = ''
  try {
    const result = await backupNow()
    backupNotice.value = `Backed up ${formatBytes(result.bytes)} in ${(result.took_ms / 1000).toFixed(1)}s.`
    await loadBackups()
  } catch (err) {
    // The 503 names the folder, because the cause is almost always the USB
    // stick not being plugged in. It is shown as written.
    backupError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    backingUp.value = false
  }
}

function backupWhen(at: string | undefined): string {
  if (!at) return ''
  const date = new Date(at)
  if (Number.isNaN(date.getTime())) return at
  return date.toLocaleString('en-PH', { dateStyle: 'medium', timeStyle: 'short' })
}

onMounted(() => {
  load()
  loadBackups()
})
</script>

<template>
  <div>
    <PageHeader
      title="Settings"
      description="Everything that can change without reinstalling. Each change takes effect immediately and is recorded in the audit log."
    />

    <Message v-if="error" severity="error" :closable="false" class="settings__banner">
      {{ error }}
    </Message>

    <section v-for="group in groups" :key="group.label" class="card card--padded settings__group">
      <h2 class="settings__group-title">{{ group.label }}</h2>
      <p v-if="group.blurb" class="settings__group-blurb">{{ group.blurb }}</p>

      <div
        v-for="setting in group.items"
        :key="setting.key"
        class="settings__row"
        :class="{ 'settings__row--blocked': !!blockedReason(setting) }"
      >
        <div class="settings__label">
          <label :for="`setting-${setting.key}`" class="settings__name">
            {{ labelFor(setting) }}
          </label>
          <p class="settings__doc">{{ setting.doc }}</p>
          <p v-if="blockedReason(setting)" class="settings__blocked">
            {{ blockedReason(setting) }}
          </p>
          <p v-if="fieldError[setting.key]" class="settings__error">
            {{ fieldError[setting.key] }}
          </p>
        </div>

        <div class="settings__control">
          <ToggleSwitch
            v-if="setting.kind_slug === 'bool'"
            :input-id="`setting-${setting.key}`"
            :model-value="setting.value === 'true'"
            :disabled="saving[setting.key] || !!blockedReason(setting)"
            @update:model-value="(v) => save(setting, v ? 'true' : 'false')"
          />

          <Select
            v-else-if="setting.kind_slug === 'choice'"
            :input-id="`setting-${setting.key}`"
            :model-value="setting.value"
            :options="choiceOptions(setting)"
            option-label="label"
            option-value="value"
            :disabled="saving[setting.key] || !!blockedReason(setting)"
            class="settings__wide"
            @update:model-value="(v) => save(setting, String(v))"
          />

          <Select
            v-else-if="setting.kind_slug === 'timezone'"
            :input-id="`setting-${setting.key}`"
            :model-value="setting.value"
            :options="timezones"
            editable
            :disabled="saving[setting.key]"
            class="settings__wide"
            @update:model-value="(v) => save(setting, String(v))"
          />

          <InputNumber
            v-else-if="setting.kind_slug === 'int'"
            :input-id="`setting-${setting.key}`"
            :model-value="Number(setting.value)"
            :use-grouping="false"
            :min="0"
            :disabled="saving[setting.key]"
            class="settings__narrow"
            @blur="(e: { value?: string | number | null }) => save(setting, String(e.value ?? setting.value))"
          />

          <InputText
            v-else
            :id="`setting-${setting.key}`"
            :model-value="setting.value"
            :placeholder="setting.kind_slug === 'time_of_day' ? 'HH:MM' : ''"
            :disabled="saving[setting.key]"
            class="settings__narrow"
            @change="(e) => save(setting, (e.target as HTMLInputElement).value)"
          />

          <span class="settings__aside">
            <!-- Basis points are how the system stores this and not how anybody
                 thinks. A manager typing 3 meaning three percent is a
                 hundredfold error in the direction nobody checks. -->
            <span v-if="setting.key === 'interest.rate_bps'" class="settings__derived">
              = {{ asPercent(setting.value) }}
            </span>
            <i v-if="saved[setting.key]" class="pi pi-check settings__saved" aria-hidden="true" />
          </span>
        </div>
      </div>
    </section>

    <BarangaySettings />

    <section class="card card--padded settings__group">
      <h2 class="settings__group-title">Backups</h2>
      <p class="settings__group-blurb">
        Written to <code>{{ backupFolder || '—' }}</code
        >. Point this at a USB stick with <code>bokkie backup -set-folder</code>, and take the
        stick off the premises — a backup on the same machine does not survive the machine.
        Times below are station time; the timestamp inside each file name is UTC, so that the
        files sort correctly in any file manager.
      </p>

      <Message v-if="backupError" severity="error" :closable="false" class="settings__banner">
        {{ backupError }}
      </Message>
      <Message v-if="backupNotice" severity="success" class="settings__banner" @close="backupNotice = ''">
        {{ backupNotice }}
      </Message>

      <div class="settings__backup-actions">
        <Button
          label="Back up now"
          icon="pi pi-save"
          severity="secondary"
          outlined
          :loading="backingUp"
          @click="runBackup"
        />
        <span class="settings__backup-count">
          {{ backups.length }} {{ backups.length === 1 ? 'file' : 'files' }}
        </span>
      </div>

      <ul v-if="backups.length" class="settings__backups">
        <li v-for="file in backups" :key="file.path" class="settings__backup">
          <span class="settings__backup-name">{{ file.name }}</span>
          <span class="settings__backup-meta">
            {{ formatBytes(file.bytes) }}
            <template v-if="file.taken_at"> · {{ backupWhen(file.taken_at) }}</template>
          </span>
        </li>
      </ul>
      <p v-else class="settings__doc">
        No backups in that folder yet. The nightly job writes one, or press Back up now.
      </p>
    </section>
  </div>
</template>

<style scoped>
.settings__banner {
  margin-bottom: 1rem;
}

.settings__group {
  margin-bottom: 1rem;
}

.settings__group-title {
  margin: 0 0 0.25rem;
  font-size: 1rem;
  font-weight: 600;
}

.settings__group-blurb {
  margin: 0 0 0.75rem;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.settings__row {
  display: flex;
  align-items: flex-start;
  gap: 1.5rem;
  padding: 0.85rem 0;
  border-top: 1px solid var(--shell-border);
}

.settings__label {
  flex: 1 1 18rem;
  min-width: 0;
}

.settings__name {
  display: block;
  font-size: 0.875rem;
  font-weight: 500;
}

.settings__doc {
  margin: 0.15rem 0 0;
  font-size: 0.8125rem;
  line-height: 1.4;
  color: var(--shell-muted);
}

.settings__blocked {
  margin: 0.3rem 0 0;
  font-size: 0.75rem;
  color: var(--shell-muted);
  font-style: italic;
}

.settings__error {
  margin: 0.3rem 0 0;
  font-size: 0.8125rem;
  color: #b91c1c;
}

.settings__control {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex: 0 0 auto;
  padding-top: 0.1rem;
}

.settings__wide {
  min-width: 18rem;
}

.settings__narrow {
  width: 9rem;
}

/*
 * InputNumber sizes its inner <input> on its own, so the 9rem above only
 * reached the wrapper and the field spilled over the "= 5% a month" beside it.
 */
.settings__narrow :deep(.p-inputnumber-input) {
  width: 100%;
  min-width: 0;
}

.settings__aside {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 4.5rem;
  font-size: 0.8125rem;
}

.settings__derived {
  color: var(--shell-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.settings__saved {
  color: #16a34a;
}

.settings__backup-actions {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding-top: 0.5rem;
  border-top: 1px solid var(--shell-border);
}

.settings__backup-count {
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.settings__backups {
  margin: 0.75rem 0 0;
  padding: 0;
  list-style: none;
  max-height: 18rem;
  overflow-y: auto;
}

.settings__backup {
  display: flex;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.4rem 0;
  border-top: 1px solid var(--shell-border);
  font-size: 0.8125rem;
}

.settings__backup-name {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings__backup-meta {
  color: var(--shell-muted);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

/* Below the counter PC's width the control drops under its label rather than
   squeezing both. A Select with an 18rem minimum and a label beside it is what
   pushes a settings form into a horizontal scroll.
   
   The label's flex-basis has to be reset along with the direction. `flex: 1 1
   18rem` is written for a row, where 18rem is a width; the moment the row
   becomes a column that same basis is a *height*, and every setting grows a
   288px gap between its label and its control. This is the second place in
   this step where a flex shorthand meant for a row leaked into a column — see
   the audit filter bar — and both were invisible until the layout wrapped. */
@media (max-width: 55rem) {
  .settings__row {
    flex-direction: column;
    gap: 0.5rem;
  }

  .settings__label {
    flex: 0 0 auto;
    width: 100%;
  }

  .settings__control {
    width: 100%;
    flex: 0 0 auto;
    flex-wrap: wrap;
  }

  .settings__wide,
  .settings__narrow {
    min-width: 0;
    width: 100%;
    flex: 1 1 12rem;
  }

  .settings__aside {
    min-width: 0;
  }
}
</style>
