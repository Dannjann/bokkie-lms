<script setup lang="ts">
/**
 * One advance line, edited where it is read: in a customer's transactions or a
 * liquidation's open advances. Placed in a table row spanning its columns.
 *
 * Saving edits the whole slip on the server — the original is voided and the
 * slip reposted under the same Ref. No. — so the parent reloads afterwards
 * rather than patching the row it showed.
 */
import { computed, ref } from 'vue'
import Button from 'primevue/button'
import DatePicker from 'primevue/datepicker'
import InputText from 'primevue/inputtext'
import { ApiError } from '@/api/client'
import { editAdvanceLine } from '@/api/advances'
import { fromISODate, parseCentavos, toISODate } from '@/format'

const props = defineProps<{
  voucherId: number
  lineId: number
  date: string
  particular: string
  amount: string
}>()

const emit = defineEmits<{ saved: []; cancel: [] }>()

const date = ref<Date>(fromISODate(props.date) ?? new Date())
const particular = ref(props.particular)
const amount = ref(props.amount)
const busy = ref(false)
const error = ref('')

const latestDate = new Date()
const valid = computed(() => parseCentavos(amount.value) !== null)

async function save(): Promise<void> {
  if (!valid.value) return
  error.value = ''
  busy.value = true
  try {
    await editAdvanceLine(props.voucherId, props.lineId, {
      date: toISODate(date.value),
      particular: particular.value.trim(),
      amount: amount.value,
    })
    emit('saved')
  } catch (err) {
    error.value = err instanceof ApiError || err instanceof Error ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="line-edit" @submit.prevent="save" @keydown.esc="emit('cancel')">
    <DatePicker
      v-model="date"
      date-format="yy-mm-dd"
      :max-date="latestDate"
      class="line-edit__date"
      aria-label="Date"
    />
    <InputText v-model="particular" class="line-edit__particular" placeholder="Particular" aria-label="Particular" />
    <InputText
      v-model="amount"
      class="line-edit__amount"
      inputmode="decimal"
      :invalid="!valid"
      aria-label="Amount"
      autofocus
    />
    <Button type="submit" label="Save" size="small" :loading="busy" :disabled="!valid" />
    <Button type="button" label="Cancel" size="small" severity="secondary" text :disabled="busy" @click="emit('cancel')" />
    <small v-if="error" class="line-edit__error">{{ error }}</small>
    <small v-else class="line-edit__hint">Saved as an edit of the whole slip; its Ref. No. stays.</small>
  </form>
</template>

<style scoped>
.line-edit {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 0;
}

.line-edit__date {
  width: 9rem;
}

.line-edit__particular {
  flex: 1 1 10rem;
  min-width: 8rem;
}

.line-edit__amount {
  width: 8rem;
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.line-edit__error,
.line-edit__hint {
  flex-basis: 100%;
  font-size: 0.75rem;
}

.line-edit__error {
  color: #b91c1c;
}

.line-edit__hint {
  color: var(--shell-muted);
}
</style>
