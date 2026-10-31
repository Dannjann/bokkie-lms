<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import InputText from 'primevue/inputtext'
import Select from 'primevue/select'
import Textarea from 'primevue/textarea'
import type { CustomerDraft } from '@/api/customers'
import { listBarangays, type Barangay } from '@/api/barangays'

/**
 * The editable half of a customer, shared by the create and edit screens so the
 * two cannot drift apart.
 *
 * The code is deliberately not here. It is assigned by the server and never
 * editable, and a field that renders it as an input — even a disabled one —
 * invites somebody to make it editable later.
 */
const draft = defineModel<CustomerDraft>({ required: true })

const props = defineProps<{
  autofocus?: boolean
  /** A new customer: the barangay decides the code, so it is asked for first. */
  isNew?: boolean
}>()

const barangays = ref<Barangay[]>([])

onMounted(async () => {
  try {
    barangays.value = await listBarangays()
  } catch {
    // The field shows empty; the server still checks whatever is sent.
  }
})

/**
 * Active barangays, plus the one this customer is already in if it has since
 * been retired — so opening an old record does not quietly blank it.
 */
const options = computed(() =>
  barangays.value.filter((b) => b.active || b.id === draft.value.barangay_id),
)

const chosen = computed(() => barangays.value.find((b) => b.id === draft.value.barangay_id) ?? null)

function optionLabel(b: Barangay): string {
  return `${b.name} (${b.prefix})`
}
</script>

<template>
  <div class="fields">
    <label class="fields__field">
      <span>Barangay</span>
      <Select
        :model-value="draft.barangay_id || null"
        :options="options"
        :option-label="optionLabel"
        option-value="id"
        :placeholder="options.length ? 'Pick a barangay' : 'No barangays yet'"
        :required="props.isNew && options.length > 0"
        :show-clear="!props.isNew"
        filter
        @update:model-value="draft.barangay_id = $event ?? 0"
      />
      <small v-if="props.isNew && chosen" class="fields__hint">
        The code will be the next {{ chosen.prefix }}-#### in {{ chosen.name }}.
      </small>
      <small v-else-if="props.isNew && !options.length" class="fields__hint">
        A manager adds barangays in Settings. Until then the code continues the BA sequence.
      </small>
      <small v-else-if="!props.isNew" class="fields__hint">
        Moving a customer to another barangay does not change their code.
      </small>
    </label>

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
