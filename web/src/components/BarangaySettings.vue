<script setup lang="ts">
/**
 * The barangay list, kept by a manager in Settings.
 *
 * A barangay's prefix becomes the front of every customer code issued in it,
 * so it can be corrected only until the first customer holds one. After that
 * it is shown, not offered: the codes are already on paper slips.
 */
import { onMounted, ref } from 'vue'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import ToggleSwitch from 'primevue/toggleswitch'
import { ApiError } from '@/api/client'
import { createBarangay, listBarangays, updateBarangay, type Barangay } from '@/api/barangays'

const barangays = ref<Barangay[]>([])
const error = ref('')
const busy = ref(false)

const name = ref('')
const prefix = ref('')

/** The row being renamed, and what it is being renamed to. */
const editing = ref<number | null>(null)
const editName = ref('')
const editPrefix = ref('')

async function load(): Promise<void> {
  try {
    barangays.value = await listBarangays()
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  }
}

async function run(action: () => Promise<unknown>): Promise<void> {
  error.value = ''
  busy.value = true
  try {
    await action()
    await load()
  } catch (err) {
    error.value = err instanceof ApiError ? err.message : 'Cannot reach the server.'
  } finally {
    busy.value = false
  }
}

function add(): void {
  void run(async () => {
    await createBarangay({ name: name.value, prefix: prefix.value, active: true })
    name.value = ''
    prefix.value = ''
  })
}

function startEdit(b: Barangay): void {
  editing.value = b.id
  editName.value = b.name
  editPrefix.value = b.prefix
}

function saveEdit(b: Barangay): void {
  void run(async () => {
    await updateBarangay(b.id, { name: editName.value, prefix: editPrefix.value, active: b.active })
    editing.value = null
  })
}

function setActive(b: Barangay, active: boolean): void {
  void run(() => updateBarangay(b.id, { name: b.name, prefix: b.prefix, active }))
}

onMounted(load)
</script>

<template>
  <section class="card card--padded barangays">
    <h2 class="barangays__title">Barangays</h2>
    <p class="barangays__blurb">
      Every new customer is given a barangay, and their code comes from it: a farmer from Pintor
      with prefix PI is PI-0001, then PI-0002. Each barangay counts on its own. A prefix can be
      corrected until the first customer holds a code with it. Customers from before barangays keep
      their BA codes.
    </p>

    <Message v-if="error" severity="error" :closable="false" class="barangays__banner">
      {{ error }}
    </Message>

    <form class="barangays__add" @submit.prevent="add">
      <InputText v-model="name" placeholder="Barangay name" aria-label="New barangay name" />
      <InputText
        v-model="prefix"
        class="barangays__prefix"
        placeholder="Prefix"
        maxlength="4"
        aria-label="New barangay code prefix"
        @input="prefix = prefix.toUpperCase()"
      />
      <Button type="submit" label="Add" icon="pi pi-plus" :disabled="busy || !name || !prefix" />
    </form>

    <table v-if="barangays.length" class="barangays__table">
      <thead>
        <tr>
          <th>Name</th>
          <th>Prefix</th>
          <th class="barangays__num">Customers</th>
          <th>Active</th>
          <th><span class="sr-only">Edit</span></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="b in barangays" :key="b.id" :class="{ 'barangays__retired': !b.active }">
          <template v-if="editing === b.id">
            <td><InputText v-model="editName" size="small" aria-label="Barangay name" /></td>
            <td>
              <InputText
                v-model="editPrefix"
                size="small"
                class="barangays__prefix"
                maxlength="4"
                :disabled="b.customers > 0"
                aria-label="Code prefix"
                @input="editPrefix = editPrefix.toUpperCase()"
              />
            </td>
          </template>
          <template v-else>
            <td>{{ b.name }}</td>
            <td class="numeric">{{ b.prefix }}-####</td>
          </template>
          <td class="barangays__num numeric">{{ b.customers }}</td>
          <td>
            <ToggleSwitch
              :model-value="b.active"
              :disabled="busy"
              :aria-label="`${b.name} active`"
              @update:model-value="setActive(b, $event)"
            />
          </td>
          <td class="barangays__actions">
            <template v-if="editing === b.id">
              <Button label="Save" size="small" :disabled="busy" @click="saveEdit(b)" />
              <Button label="Cancel" size="small" severity="secondary" text @click="editing = null" />
            </template>
            <Button
              v-else
              icon="pi pi-pencil"
              size="small"
              severity="secondary"
              text
              :aria-label="`Edit ${b.name}`"
              @click="startEdit(b)"
            />
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else class="barangays__empty">
      No barangays yet. Until one is added, new customers continue the BA sequence.
    </p>
  </section>
</template>

<style scoped>
.barangays {
  margin-bottom: 1rem;
}

.barangays__title {
  margin: 0 0 0.25rem;
  font-size: 1rem;
  font-weight: 600;
}

.barangays__blurb {
  margin: 0 0 0.75rem;
  font-size: 0.8125rem;
}

.barangays__banner {
  margin-bottom: 0.75rem;
}

.barangays__add {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}

.barangays__prefix {
  width: 6rem;
  text-transform: uppercase;
}

.barangays__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.barangays__table th {
  padding: 0.4rem 0.5rem;
  text-align: left;
  font-size: 0.8125rem;
  font-weight: 600;
  color: var(--shell-muted);
  border-bottom: 1px solid var(--shell-border);
}

.barangays__table td {
  padding: 0.35rem 0.5rem;
  border-bottom: 1px solid var(--shell-border);
  vertical-align: middle;
}

.barangays__num {
  text-align: right;
}

.barangays__table th.barangays__num {
  text-align: right;
}

.barangays__actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.25rem;
}

.barangays__retired td {
  color: var(--shell-muted);
}

.barangays__empty {
  margin: 0;
  font-size: 0.8125rem;
  color: var(--shell-muted);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
</style>
