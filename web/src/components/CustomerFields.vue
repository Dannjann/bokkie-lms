<script setup lang="ts">
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import type { CustomerDraft } from '@/api/customers'

/**
 * The editable half of a customer, shared by the create and edit screens so the
 * two cannot drift apart.
 *
 * The code is deliberately not here. It is assigned by the server and never
 * editable, and a field that renders it as an input — even a disabled one —
 * invites somebody to make it editable later.
 */
const draft = defineModel<CustomerDraft>({ required: true })

defineProps<{ autofocus?: boolean }>()
</script>

<template>
  <div class="fields">
    <label class="fields__field">
      <span>Name</span>
      <InputText
        v-model="draft.name"
        :autofocus="autofocus"
        required
        autocapitalize="words"
        spellcheck="false"
      />
    </label>

    <label class="fields__field">
      <span>Address</span>
      <InputText v-model="draft.address" autocapitalize="words" spellcheck="false" />
    </label>

    <label class="fields__field">
      <span>Phone</span>
      <InputText
        v-model="draft.cell_no"
        type="tel"
        inputmode="tel"
        autocomplete="tel"
        placeholder="0917 123 4567"
      />
      <small class="fields__hint">
        Punctuation is ignored — 0917-123-4567 and 0917 123 4567 are the same number.
      </small>
    </label>

    <label class="fields__field">
      <span>Notes</span>
      <Textarea v-model="draft.notes" rows="3" auto-resize />
    </label>
  </div>
</template>

<style scoped>
.fields {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.fields__field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  font-size: 0.875rem;
}

.fields__hint {
  color: var(--p-surface-500);
  font-size: 0.75rem;
}
</style>
