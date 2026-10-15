<script setup lang="ts">
/**
 * The screen the station's window opens on.
 *
 * Four things and no more: is it running, where to reach it, when it last
 * backed up, and how to stop it. The person reading this is usually standing
 * at the counter with somebody waiting, so anything that is not one of those
 * four belongs on another screen.
 */
import { onMounted, onUnmounted, ref } from 'vue'
import { fetchStatus, stopStation, type Status } from '@/api/control'

const status = ref<Status | null>(null)
const error = ref('')
const stopping = ref(false)
const confirmingStop = ref(false)

let timer: number | undefined

async function load() {
  try {
    status.value = await fetchStatus()
    error.value = ''
  } catch (e) {
    // While stopping, a failed poll is the station going down on purpose and
    // must not be reported as a fault.
    if (!stopping.value) {
      error.value = (e as Error).message
      // Cleared, not left behind. Holding the last good status meant the green
      // dot and "Bokkie is running" stayed on screen for as long as the window
      // was open after the process had died — on the only status display the
      // station has, to the person deciding whether the counters can work.
      status.value = null
    }
  }
}

async function stop() {
  stopping.value = true
  confirmingStop.value = false
  try {
    await stopStation()
  } catch (e) {
    stopping.value = false
    error.value = (e as Error).message
  }
}

/** The LAN address opens in an ordinary tab, which is the point: it answers
 *  "does the name actually work" from the machine that knows the answer. */
function openLan() {
  if (status.value) window.open(status.value.lanAddress, '_blank')
}

function backupText(s: Status): string {
  if (!s.lastBackup) return 'none yet'
  return new Date(s.lastBackup).toLocaleString()
}

onMounted(() => {
  load()
  timer = window.setInterval(load, 5000)
})
onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <main class="station">
    <h1>Bokkie</h1>

    <p v-if="stopping" class="state stopped">
      <span class="dot stopped"></span>
      Bokkie is stopping. You can close this window.
    </p>
    <p v-else-if="status" class="state running">
      <span class="dot running"></span>
      Bokkie is running — version {{ status.version }}
    </p>
    <p v-else-if="error" class="state stopped">
      <span class="dot stopped"></span>
      Bokkie is not answering.
    </p>
    <p v-else class="state">Checking…</p>

    <section v-if="status && !stopping" class="card">
      <h2>Open it at the station</h2>
      <p class="address">{{ status.lanAddress }}</p>
      <button type="button" class="primary" @click="openLan">Open Bokkie</button>
      <p class="hint">
        This is the address the counter computers use. If it does not open,
        run the check below — the name may be pointing at another machine.
      </p>
    </section>

    <section v-if="status && !stopping" class="card">
      <h2>Last backup</h2>
      <p class="address">{{ backupText(status) }}</p>
      <p class="hint">
        A backup is taken when Bokkie starts and again when you close this
        window, so closing it is itself a save.
      </p>
    </section>

    <section v-if="status && !stopping" class="card danger">
      <h2>Stop Bokkie</h2>
      <p class="hint">
        The counter computers will lose Bokkie until somebody opens this window
        again. Nothing is deleted.
      </p>
      <button v-if="!confirmingStop" type="button" @click="confirmingStop = true">
        Stop Bokkie
      </button>
      <template v-else>
        <p class="confirm">Stop Bokkie now?</p>
        <button type="button" class="danger" @click="stop">Yes, stop it</button>
        <button type="button" @click="confirmingStop = false">Cancel</button>
      </template>
    </section>

    <nav class="more">
      <RouterLink to="/station/check">Check why Bokkie cannot be reached</RouterLink>
    </nav>

    <p class="footnote">
      Bokkie runs only while this window is open. After the computer restarts,
      somebody has to open it again.
    </p>
  </main>
</template>

<style scoped>
.station { max-width: 34rem; margin: 0 auto; padding: 2rem 1rem 4rem; }
h1 { font-size: 2rem; margin: 0 0 1rem; }
h2 { font-size: 1rem; margin: 0 0 .5rem; }

.state { display: flex; align-items: center; gap: .6rem; font-size: 1.15rem; margin: 0 0 2rem; }
.dot { width: .7rem; height: .7rem; border-radius: 50%; display: inline-block; }
.dot.running { background: #2f9e5f; }
.dot.stopped { background: #b4453a; }

.card { border: 1px solid var(--p-content-border-color, #ddd); border-radius: .5rem; padding: 1rem 1.1rem; margin-bottom: 1rem; }
.card.danger { border-color: #d8b4ae; }
.address { font-size: 1.15rem; margin: 0 0 .75rem; word-break: break-all; }
.hint { font-size: .9rem; opacity: .75; margin: .5rem 0 0; }
.confirm { margin: .5rem 0; font-weight: 600; }

button { font: inherit; padding: .5rem .9rem; border-radius: .35rem; border: 1px solid currentColor; background: transparent; cursor: pointer; margin-right: .5rem; }
button.primary { background: var(--p-primary-color, #0270c4); border-color: var(--p-primary-color, #0270c4); color: #fff; }
button.danger { background: #b4453a; border-color: #b4453a; color: #fff; }

.more { margin: 1.5rem 0; }
.footnote { font-size: .85rem; opacity: .7; border-top: 1px solid var(--p-content-border-color, #ddd); padding-top: 1rem; }

@media (max-width: 460px) {
  .station { padding: 1.25rem 1rem 3rem; }
  h1 { font-size: 1.6rem; }
}
</style>
