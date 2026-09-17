<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import DataTable from 'primevue/datatable'
import Column from 'primevue/column'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Menu from 'primevue/menu'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import PageHeader from '@/components/PageHeader.vue'
import { ApiError } from '@/api/client'
import {
  createUser,
  listUsers,
  resetUserPassword,
  roleDescriptions,
  roleOptions,
  setUserActive,
  setUserRole,
  type Role,
  type User,
} from '@/api/users'
import { useSessionStore } from '@/stores/session'

const { user: me } = storeToRefs(useSessionStore())

const users = ref<User[]>([])
const loading = ref(false)
const error = ref('')
const notice = ref('')

// Deactivated accounts are shown, greyed, rather than filtered out. A roster
// that hides what it has turned off is a roster somebody re-creates an account
// in — and then there are two, and the audit log has to be read to work out
// which one is the real person.
const sorted = computed(() =>
  [...users.value].sort((a, b) => {
    if (a.active !== b.active) return a.active ? -1 : 1
    return a.display_name.localeCompare(b.display_name)
  }),
)

const activeManagers = computed(
  () => users.value.filter((u) => u.active && u.role === 'manager').length,
)

/**
 * Why a row's controls are disabled, or empty when they are not.
 *
 * The server enforces both of these and will refuse anyway. Saying so before
 * the click is the difference between a control that explains itself and one
 * that appears broken.
 *
 * The last-manager case is tested *before* the self case, and the order is the
 * point. When both apply — you are the only manager left, looking at your own
 * row — "you cannot do this to yourself" is true and useless, because it
 * implies somebody else could and there is nobody else. "Make somebody else a
 * manager first" is the sentence that gets the person unstuck.
 */
function whyBlocked(user: User, action: 'deactivate' | 'demote'): string {
  if (user.role === 'manager' && user.active && activeManagers.value <= 1) {
    return 'This is the last manager who can still sign in. Make somebody else a manager first.'
  }
  if (user.id === me.value?.id) {
    return action === 'deactivate'
      ? 'You cannot deactivate your own account — another manager has to do it.'
      : 'You cannot take away your own manager role — another manager has to do it.'
  }
  return ''
}

/**
 * One overflow menu per row rather than three buttons in a line.
 *
 * Three full-text buttons is about 380px of table, and the station's counter
 * PCs run this at 1024px: at that width the last of them — Deactivate, the one
 * that matters — is clipped off the right edge of the card, and at 390px the
 * whole group is unreachable. A single glyph is the same width at every size.
 */
const menu = ref<InstanceType<typeof Menu> | null>(null)
const menuRow = ref<User | null>(null)

const menuItems = computed(() => {
  const user = menuRow.value
  if (!user) return []

  const demoting = user.role === 'manager'
  const roleBlocked = demoting ? whyBlocked(user, 'demote') : ''
  const activeBlocked = user.active ? whyBlocked(user, 'deactivate') : ''

  return [
    {
      label: demoting ? 'Make encoder' : 'Make manager',
      icon: demoting ? 'pi pi-user-minus' : 'pi pi-user-plus',
      disabled: !!roleBlocked,
      command: () => toggleRole(user),
    },
    { label: 'Reset password…', icon: 'pi pi-key', command: () => openReset(user) },
    {
      label: user.active ? 'Deactivate' : 'Reactivate',
      icon: user.active ? 'pi pi-ban' : 'pi pi-check-circle',
      disabled: !!activeBlocked,
      command: () => toggleActive(user),
    },
  ]
})

/**
 * Why the menu's greyed items are greyed, shown under them.
 *
 * A disabled row in a popup menu has nowhere to put a tooltip somebody will
 * find, and "the button does nothing" is the complaint that follows. The server
 * refuses these three anyway; this only saves the round trip and explains it.
 */
const menuBlockedReason = computed(() => {
  const user = menuRow.value
  if (!user) return ''
  return (
    (user.active ? whyBlocked(user, 'deactivate') : '') ||
    (user.role === 'manager' ? whyBlocked(user, 'demote') : '')
  )
})

function openMenu(event: Event, user: User): void {
  menuRow.value = user
  menu.value?.toggle(event)
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    users.value = (await listUsers()).users
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
    users.value = []
  } finally {
    loading.value = false
  }
}

const busyId = ref<number | null>(null)

/** Runs one row action, replacing the row with what the server says it now is. */
async function act(user: User, run: () => Promise<User>, done: string): Promise<void> {
  busyId.value = user.id
  error.value = ''
  notice.value = ''
  try {
    const updated = await run()
    users.value = users.value.map((u) => (u.id === updated.id ? updated : u))
    notice.value = done
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busyId.value = null
  }
}

function toggleActive(user: User): void {
  const next = !user.active
  act(
    user,
    () => setUserActive(user.id, next),
    next
      ? `${user.display_name} can sign in again.`
      : `${user.display_name} is deactivated and has been signed out.`,
  )
}

function toggleRole(user: User): void {
  const next: Role = user.role === 'manager' ? 'encoder' : 'manager'
  act(
    user,
    () => setUserRole(user.id, next),
    `${user.display_name} is now ${next === 'manager' ? 'a manager' : 'an encoder'}.`,
  )
}

// --- New account -----------------------------------------------------------

const newOpen = ref(false)
const draft = ref({ username: '', display_name: '', password: '', role: 'encoder' as Role })
const newError = ref('')
const saving = ref(false)

function openNew(): void {
  draft.value = { username: '', display_name: '', password: '', role: 'encoder' }
  newError.value = ''
  newOpen.value = true
}

async function save(): Promise<void> {
  saving.value = true
  newError.value = ''
  try {
    const created = await createUser({
      username: draft.value.username.trim().toLowerCase(),
      display_name: draft.value.display_name.trim(),
      password: draft.value.password,
      role: draft.value.role,
    })
    users.value = [...users.value, created]
    notice.value = `${created.display_name} can now sign in as ${created.username}.`
    newOpen.value = false
  } catch (err) {
    newError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    saving.value = false
  }
}

// --- Reset a password ------------------------------------------------------

const resetting = ref<User | null>(null)
const resetPassword = ref('')
const resetConfirm = ref('')
const resetError = ref('')
const resetBusy = ref(false)

function openReset(user: User): void {
  resetting.value = user
  resetPassword.value = ''
  resetConfirm.value = ''
  resetError.value = ''
}

async function submitReset(): Promise<void> {
  const target = resetting.value
  if (!target) return

  if (resetPassword.value !== resetConfirm.value) {
    resetError.value = 'The two passwords do not match.'
    return
  }

  resetBusy.value = true
  resetError.value = ''
  try {
    const result = await resetUserPassword(target.id, resetPassword.value)
    users.value = users.value.map((u) => (u.id === result.user.id ? result.user : u))
    notice.value =
      result.sessions_ended > 0
        ? `${target.display_name}'s password is changed, and they were signed out of ` +
          `${result.sessions_ended} ${result.sessions_ended === 1 ? 'machine' : 'machines'}.`
        : `${target.display_name}'s password is changed.`
    resetting.value = null
  } catch (err) {
    resetError.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    resetBusy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div>
    <PageHeader
      title="Users"
      description="Who can sign in, and what they may do. Accounts are never deleted — deactivate one instead."
    >
      <template #actions>
        <Button label="New user" icon="pi pi-plus" @click="openNew" />
      </template>
    </PageHeader>

    <Message v-if="error" severity="error" :closable="false" class="users__banner">
      {{ error }}
    </Message>
    <Message v-if="notice" severity="success" class="users__banner" @close="notice = ''">
      {{ notice }}
    </Message>

    <div class="card">
      <DataTable :value="sorted" :loading="loading" data-key="id" class="users__table">
        <template #empty>
          <p class="users__empty">No accounts yet.</p>
        </template>

        <Column header="Name">
          <template #body="{ data }">
            <span class="users__name" :class="{ 'users__name--off': !data.active }">
              {{ data.display_name }}
            </span>
            <span v-if="data.id === me?.id" class="users__you">you</span>
          </template>
        </Column>

        <Column field="username" header="Username" class="users__username" />

        <Column header="Role" class="users__role">
          <template #body="{ data }">
            <Tag
              :value="data.role === 'manager' ? 'Manager' : 'Encoder'"
              :severity="data.role === 'manager' ? 'info' : 'secondary'"
            />
          </template>
        </Column>

        <Column header="Status" class="users__status">
          <template #body="{ data }">
            <Tag v-if="!data.active" severity="secondary" value="Deactivated" />
            <span v-else class="users__ok">Active</span>
          </template>
        </Column>

        <Column header="" class="users__actions">
          <template #body="{ data }">
            <Button
              icon="pi pi-ellipsis-v"
              severity="secondary"
              text
              rounded
              size="small"
              :loading="busyId === data.id"
              :aria-label="`Actions for ${data.display_name}`"
              aria-haspopup="true"
              aria-controls="users-row-menu"
              @click="openMenu($event, data)"
            />
          </template>
        </Column>
      </DataTable>
    </div>

    <Menu id="users-row-menu" ref="menu" :model="menuItems" popup>
      <template #end>
        <p v-if="menuBlockedReason" class="users__menu-reason">{{ menuBlockedReason }}</p>
      </template>
    </Menu>

    <!-- New account -->
    <Dialog
      v-model:visible="newOpen"
      modal
      header="New user"
      :style="{ width: '28rem' }"
      :breakpoints="{ '32rem': '92vw' }"
    >
      <form class="users__form" @submit.prevent="save">
        <Message v-if="newError" severity="error" :closable="false">{{ newError }}</Message>

        <div class="users__field">
          <label for="new-user-name">Full name</label>
          <InputText id="new-user-name" v-model="draft.display_name" fluid />
        </div>

        <div class="users__field">
          <label for="new-user-username">Username</label>
          <InputText
            id="new-user-username"
            v-model="draft.username"
            autocapitalize="none"
            autocorrect="off"
            spellcheck="false"
            fluid
          />
          <small>Lowercase letters, numbers, dot, dash or underscore. 3 to 32 characters.</small>
        </div>

        <div class="users__field">
          <label for="new-user-password">Password</label>
          <InputText
            id="new-user-password"
            v-model="draft.password"
            type="password"
            autocomplete="new-password"
            fluid
          />
          <small>At least 8 characters. Tell them in person, not on a note.</small>
        </div>

        <div class="users__field">
          <label for="new-user-role">Role</label>
          <Select
            id="new-user-role"
            v-model="draft.role"
            :options="roleOptions"
            option-label="label"
            option-value="value"
            fluid
          />
          <small>{{ roleDescriptions[draft.role] }}</small>
        </div>

        <div class="users__form-actions">
          <Button
            type="button"
            label="Cancel"
            severity="secondary"
            text
            :disabled="saving"
            @click="newOpen = false"
          />
          <Button
            type="submit"
            label="Create account"
            :loading="saving"
            :disabled="!draft.username || !draft.password"
          />
        </div>
      </form>
    </Dialog>

    <!-- Reset a password -->
    <Dialog
      :visible="resetting !== null"
      modal
      :header="`Reset ${resetting?.display_name}'s password`"
      :style="{ width: '26rem' }"
      :breakpoints="{ '30rem': '92vw' }"
      @update:visible="resetting = null"
    >
      <form class="users__form" @submit.prevent="submitReset">
        <Message v-if="resetError" severity="error" :closable="false">{{ resetError }}</Message>

        <div class="users__field">
          <label for="reset-password">New password</label>
          <InputText
            id="reset-password"
            v-model="resetPassword"
            type="password"
            autocomplete="new-password"
            fluid
          />
          <small>At least 8 characters.</small>
        </div>

        <div class="users__field">
          <label for="reset-confirm">New password again</label>
          <InputText
            id="reset-confirm"
            v-model="resetConfirm"
            type="password"
            autocomplete="new-password"
            fluid
          />
        </div>

        <p class="users__note">
          They will be signed out everywhere and will need this password to get back in.
        </p>

        <div class="users__form-actions">
          <Button
            type="button"
            label="Cancel"
            severity="secondary"
            text
            :disabled="resetBusy"
            @click="resetting = null"
          />
          <Button
            type="submit"
            label="Reset password"
            severity="danger"
            :loading="resetBusy"
            :disabled="!resetPassword || !resetConfirm"
          />
        </div>
      </form>
    </Dialog>
  </div>
</template>

<style scoped>
.users__banner {
  margin-bottom: 1rem;
}

.users__name--off {
  color: var(--shell-muted);
}

.users__you {
  margin-left: 0.4rem;
  padding: 0.05rem 0.35rem;
  border-radius: 4px;
  background: var(--p-surface-100);
  font-size: 0.6875rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--shell-muted);
}

.users__ok {
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.users__table :deep(.users__username) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.8125rem;
  white-space: nowrap;
}

.users__table :deep(.users__role),
.users__table :deep(.users__status) {
  width: 1%;
  white-space: nowrap;
}

.users__table :deep(.users__actions) {
  width: 1%;
}

.users__menu-reason {
  margin: 0;
  padding: 0.5rem 0.75rem 0.6rem;
  max-width: 15rem;
  border-top: 1px solid var(--shell-border);
  font-size: 0.75rem;
  line-height: 1.35;
  color: var(--shell-muted);
}

.users__table :deep(.p-datatable-table-container) {
  border-radius: 0;
}

.users__empty {
  margin: 0;
  padding: 1.5rem 0;
  text-align: center;
  color: var(--shell-muted);
}

.users__form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.users__field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.users__field label {
  font-size: 0.875rem;
  font-weight: 500;
}

.users__field small {
  color: var(--shell-muted);
}

.users__note {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.users__form-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}

/* Below the counter PC's width the username is the column to give up: it is
   the only one nobody reads off the screen to somebody else. */
@media (max-width: 45rem) {
  .users__table :deep(.users__username) {
    display: none;
  }
}
</style>
