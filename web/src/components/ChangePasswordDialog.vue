<script setup lang="ts">
import { ref, watch } from 'vue'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Message from 'primevue/message'
import { ApiError } from '@/api/client'
import { changeOwnPassword } from '@/api/users'

/**
 * Changing your own password.
 *
 * The current one is asked for because the server requires it, and the server
 * requires it because a session is not proof of the password behind it — a
 * browser left unlocked at a counter would otherwise be enough to lock the
 * owner out of their own account.
 *
 * Confirming the new one is this screen's job and not the server's: the server
 * has no way to tell a typo from a decision, and a mistyped password here would
 * lock somebody out of a system whose only reset needs a manager standing next
 * to them.
 */
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ changed: [sessionsEnded: number] }>()

const current = ref('')
const next = ref('')
const confirm = ref('')
const busy = ref(false)
const error = ref('')

// Every field cleared on the way in, not on the way out. Clearing on close
// leaves a password sitting in memory for as long as the dialog is shut, and
// this runs on shared counter PCs.
watch(open, (isOpen) => {
  if (!isOpen) return
  current.value = ''
  next.value = ''
  confirm.value = ''
  error.value = ''
})

async function submit(): Promise<void> {
  error.value = ''

  if (next.value !== confirm.value) {
    error.value = 'The two new passwords do not match.'
    return
  }
  if (next.value === current.value) {
    error.value = 'The new password is the same as the current one.'
    return
  }

  busy.value = true
  try {
    const result = await changeOwnPassword(current.value, next.value)
    emit('changed', result.sessions_ended)
    open.value = false
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:visible="open"
    modal
    header="Change your password"
    :style="{ width: '26rem' }"
    :breakpoints="{ '30rem': '92vw' }"
  >
    <form class="change-password" @submit.prevent="submit">
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

      <div class="change-password__field">
        <label for="change-password-current">Current password</label>
        <InputText
          id="change-password-current"
          v-model="current"
          type="password"
          autocomplete="current-password"
          fluid
        />
      </div>

      <div class="change-password__field">
        <label for="change-password-new">New password</label>
        <InputText
          id="change-password-new"
          v-model="next"
          type="password"
          autocomplete="new-password"
          fluid
        />
        <small>At least 8 characters.</small>
      </div>

      <div class="change-password__field">
        <label for="change-password-confirm">New password again</label>
        <InputText
          id="change-password-confirm"
          v-model="confirm"
          type="password"
          autocomplete="new-password"
          fluid
        />
      </div>

      <p class="change-password__note">
        You will stay signed in here. Any other machine you are signed in on will
        be signed out.
      </p>

      <div class="change-password__actions">
        <Button
          type="button"
          label="Cancel"
          severity="secondary"
          text
          :disabled="busy"
          @click="open = false"
        />
        <Button
          type="submit"
          label="Change password"
          :loading="busy"
          :disabled="!current || !next || !confirm"
        />
      </div>
    </form>
  </Dialog>
</template>

<style scoped>
.change-password {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.change-password__field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.change-password__field label {
  font-size: 0.875rem;
  font-weight: 500;
}

.change-password__field small {
  color: var(--shell-muted);
}

.change-password__note {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.change-password__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
</style>
