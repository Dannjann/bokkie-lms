<script setup lang="ts">
/**
 * `bokkie doctor`, rendered.
 *
 * The same checks and the same words the console prints — there is one set of
 * them, in internal/diagnose, so the screen and the terminal cannot tell
 * somebody two different stories about the same machine.
 *
 * A note is grey rather than red on purpose. A station deliberately set to
 * serve only its own screen is working as asked, and colouring that like a
 * fault sends somebody looking for a bug instead of for the setting.
 */
import { onMounted, ref } from 'vue'
import { runCheck, type CheckReport } from '@/api/control'

const report = ref<CheckReport | null>(null)
const error = ref('')
const running = ref(false)

async function run() {
  running.value = true
  try {
    report.value = await runCheck()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    running.value = false
  }
}

const label: Record<string, string> = { ok: 'ok', note: 'note', problem: 'needs attention' }

onMounted(run)
</script>

<template>
  <main class="check">
    <RouterLink to="/station" class="back">← Back</RouterLink>
    <h1>Check</h1>
    <p class="hint">This changes nothing. It looks, and tells you what it found.</p>

    <p v-if="running">Checking…</p>
    <p v-else-if="error" class="problem">{{ error }}</p>

    <ol v-if="report" class="checks">
      <li v-for="check in report.checks" :key="check.name" :class="check.state">
        <div class="row">
          <span class="chip" :class="check.state">{{ label[check.state] }}</span>
          <span class="name">{{ check.name }}</span>
        </div>
        <p v-if="check.detail" class="detail">{{ check.detail }}</p>
        <pre v-if="check.fix" class="fix">{{ check.fix }}</pre>
      </li>
    </ol>

    <p v-if="report?.reach" class="reach">
      Open this address at the station: <strong>{{ report.reach }}</strong>
    </p>

    <button type="button" :disabled="running" @click="run">Check again</button>
  </main>
</template>

<style scoped>
.check { max-width: 40rem; margin: 0 auto; padding: 2rem 1rem 4rem; }
.back { display: inline-block; margin-bottom: 1rem; }
h1 { font-size: 1.8rem; margin: 0 0 .5rem; }
.hint { opacity: .75; margin: 0 0 1.5rem; }

.checks { list-style: none; padding: 0; margin: 0 0 1.5rem; }
.checks li { border-left: 3px solid #ccc; padding: .6rem 0 .6rem .9rem; margin-bottom: 1rem; }
.checks li.problem { border-left-color: #b4453a; }
.checks li.ok { border-left-color: #2f9e5f; }

.row { display: flex; align-items: baseline; gap: .6rem; flex-wrap: wrap; }
.name { font-weight: 600; }
.chip { font-size: .72rem; text-transform: uppercase; letter-spacing: .04em; padding: .1rem .45rem; border-radius: .25rem; border: 1px solid currentColor; white-space: nowrap; }
.chip.ok { color: #2f9e5f; }
.chip.note { color: #6b6b6b; }
.chip.problem { color: #b4453a; }

.detail { margin: .4rem 0 0; font-size: .92rem; opacity: .85; word-break: break-word; }
.fix { margin: .6rem 0 0; font-size: .88rem; white-space: pre-wrap; background: rgba(0, 0, 0, .04); padding: .6rem .75rem; border-radius: .3rem; }
.reach { padding: .75rem; border: 1px solid #2f9e5f; border-radius: .35rem; }
.problem { color: #b4453a; }

button { font: inherit; padding: .5rem .9rem; border-radius: .35rem; border: 1px solid currentColor; background: transparent; cursor: pointer; }
</style>
