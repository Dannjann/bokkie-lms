<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { navigation, type NavItem } from '@/nav'
import { useSessionStore } from '@/stores/session'
import Menu from 'primevue/menu'
import type { MenuItem } from 'primevue/menuitem'
import ChangePasswordDialog from '@/components/ChangePasswordDialog.vue'

const session = useSessionStore()
const { user, isManager } = storeToRefs(session)
const route = useRoute()
const router = useRouter()

defineEmits<{ navigate: [] }>()

const sections = computed(() =>
  navigation
    .map((section) => ({
      ...section,
      items: section.items.filter((item) => !item.manager || isManager.value),
    }))
    .filter((section) => section.items.length > 0),
)

/**
 * The link whose path is the longest prefix of where we are.
 *
 * Prefix matching alone would light both "Customers" and "Import customers" on
 * /customers/import. Longest-match picks the more specific one, while
 * /customers/new — which has no link of its own — still lights "Customers".
 */
const activePath = computed(() => {
  const here = route.path

  let best = ''
  for (const section of navigation) {
    for (const item of section.items) {
      if (!item.to || item.to === '/') continue
      if (here !== item.to && !here.startsWith(`${item.to}/`)) continue
      if (item.to.length > best.length) best = item.to
    }
  }
  return best || (here === '/' ? '/' : '')
})

function isActive(item: NavItem): boolean {
  return item.to !== undefined && item.to === activePath.value
}

const initials = computed(() => {
  const name = user.value?.display_name ?? ''
  const parts = name.split(/\s+/).filter(Boolean)
  if (parts.length === 0) return '?'
  return (parts[0]![0]! + (parts.length > 1 ? parts.at(-1)![0]! : '')).toUpperCase()
})

const busy = ref(false)

// Changing your own password lives here rather than on the Users screen,
// because everybody has one and only a manager has that screen. The dialog
// asks for the current password — the server insists, since a session is not
// proof of the password behind it.
const passwordOpen = ref(false)

// Lock, not a timeout. The station PC is signed in from opening to closing --
// that is the real usage -- so the way to leave it safely is a one-click action
// that keeps the username, not an idle timer that logs people out mid-payment
// and teaches them to share passwords instead.
async function lock(): Promise<void> {
  busy.value = true
  try {
    await session.lock()
    await router.push({ name: 'login' })
  } finally {
    busy.value = false
  }
}

/**
 * The account menu. Lock stays a button of its own beside it, because it is
 * the one used many times a day; changing a password and signing out are rare,
 * and three icon buttons in a row had squeezed everyone's name down to
 * "Maria Sa…".
 */
const accountMenu = ref<InstanceType<typeof Menu> | null>(null)

const accountItems = computed<MenuItem[]>(() => [
  { label: 'Change password…', icon: 'pi pi-key', command: () => (passwordOpen.value = true) },
  { separator: true },
  { label: 'Sign out', icon: 'pi pi-sign-out', command: () => void signOut() },
])

async function signOut(): Promise<void> {
  busy.value = true
  try {
    await session.logout()
    await router.push({ name: 'login' })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="sidebar">
    <RouterLink to="/" class="sidebar__brand" @click="$emit('navigate')">
      <span class="sidebar__mark" aria-hidden="true">B</span>
      <span class="sidebar__brand-text">
        <span class="sidebar__wordmark">Bokkie</span>
        <span class="sidebar__station">TLS Palay &amp; Corn</span>
      </span>
    </RouterLink>

    <nav class="sidebar__nav" aria-label="Sections">
      <div v-for="(section, index) in sections" :key="section.label ?? index" class="sidebar__section">
        <p v-if="section.label" class="sidebar__section-label">{{ section.label }}</p>

        <template v-for="item in section.items" :key="item.label">
          <RouterLink
            v-if="item.to"
            :to="item.to"
            class="sidebar__item"
            :class="{ 'sidebar__item--active': isActive(item) }"
            :aria-current="isActive(item) ? 'page' : undefined"
            @click="$emit('navigate')"
          >
            <i :class="item.icon" aria-hidden="true" />
            <span>{{ item.label }}</span>
          </RouterLink>

          <!--
            Not a disabled <button>: there is nothing to press. It is a label
            that says where something will be, and it should not be reachable
            by tab or announced as a control.
          -->
          <span v-else class="sidebar__item sidebar__item--soon">
            <i :class="item.icon" aria-hidden="true" />
            <span>{{ item.label }}</span>
            <span class="sidebar__soon">soon</span>
          </span>
        </template>
      </div>
    </nav>

    <div v-if="user" class="sidebar__user">
      <span class="sidebar__avatar" aria-hidden="true">{{ initials }}</span>
      <span class="sidebar__identity">
        <span class="sidebar__name">{{ user.display_name }}</span>
        <span class="sidebar__role">{{ user.role === 'manager' ? 'Manager' : 'Encoder' }}</span>
      </span>
      <button
        type="button"
        class="sidebar__action"
        title="Lock — keeps you signed in, asks for your password to come back"
        aria-label="Lock"
        :disabled="busy"
        @click="lock"
      >
        <i class="pi pi-lock" aria-hidden="true" />
      </button>
      <button
        type="button"
        class="sidebar__action"
        title="Account"
        aria-label="Account"
        aria-haspopup="menu"
        aria-controls="account-menu"
        :disabled="busy"
        @click="accountMenu?.toggle($event)"
      >
        <i class="pi pi-ellipsis-h" aria-hidden="true" />
      </button>
      <Menu id="account-menu" ref="accountMenu" :model="accountItems" popup />
    </div>

    <ChangePasswordDialog v-model="passwordOpen" />
  </div>
</template>

<style scoped>
.sidebar {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--shell-nav-bg);
  color: var(--shell-nav-fg);
}

.sidebar__brand {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  height: var(--shell-topbar-height);
  padding: 0 1rem;
  flex: none;
  color: inherit;
  text-decoration: none;
}

.sidebar__brand:hover {
  text-decoration: none;
}

.sidebar__brand:focus-visible {
  outline: 2px solid var(--shell-nav-accent);
  outline-offset: -2px;
}

.sidebar__mark {
  display: grid;
  place-items: center;
  width: 2rem;
  height: 2rem;
  flex: none;
  border-radius: 8px;
  background: linear-gradient(160deg, var(--p-primary-400), var(--p-primary-600));
  color: #fff;
  font-weight: 700;
  font-size: 1rem;
}

.sidebar__brand-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.sidebar__wordmark {
  font-weight: 600;
  font-size: 0.9375rem;
  line-height: 1.2;
  color: var(--shell-nav-fg-hover);
}

.sidebar__station {
  font-size: 0.75rem;
  line-height: 1.3;
  color: var(--shell-nav-fg-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sidebar__nav {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0.5rem 0.625rem 1rem;
}

.sidebar__section + .sidebar__section {
  margin-top: 1.125rem;
}

/* Sentence case, the way a platform sidebar heads its groups. */
.sidebar__section-label {
  margin: 0 0 0.25rem;
  padding: 0 0.625rem;
  font-size: 0.75rem;
  font-weight: 600;
  color: var(--shell-nav-fg-muted);
}

.sidebar__item {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  min-height: var(--shell-hit);
  padding: 0 0.625rem;
  border-radius: 8px;
  font-size: 0.875rem;
  color: inherit;
  text-decoration: none;
}

.sidebar__item i {
  font-size: 0.9375rem;
  width: 1.125rem;
  flex: none;
  color: var(--p-primary-color);
}

a.sidebar__item:hover {
  background: var(--shell-nav-hover-bg);
  color: var(--shell-nav-fg-hover);
  text-decoration: none;
}

a.sidebar__item:focus-visible {
  outline: 2px solid var(--shell-nav-accent);
  outline-offset: -2px;
}

.sidebar__item--active,
a.sidebar__item--active:hover {
  background: var(--shell-nav-active-bg);
  color: var(--shell-nav-fg-active);
  font-weight: 600;
}

.sidebar__item--soon {
  color: var(--shell-nav-fg-muted);
  cursor: default;
}

.sidebar__item--soon i {
  color: inherit;
}

.sidebar__soon {
  margin-left: auto;
  padding: 0.05rem 0.4rem;
  border-radius: 999px;
  background: var(--shell-nav-bg-deep);
  font-size: 0.6875rem;
}

.sidebar__user {
  flex: none;
  display: flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.625rem 0.625rem 0.625rem 0.75rem;
  border-top: 1px solid var(--shell-nav-border);
}

.sidebar__avatar {
  display: grid;
  place-items: center;
  width: 2rem;
  height: 2rem;
  flex: none;
  margin-right: 0.25rem;
  border-radius: 50%;
  background: var(--p-primary-100);
  color: var(--p-primary-700);
  font-size: 0.75rem;
  font-weight: 600;
}

.sidebar__identity {
  display: flex;
  flex-direction: column;
  min-width: 0;
  margin-right: auto;
}

.sidebar__name {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--shell-nav-fg-hover);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sidebar__role {
  font-size: 0.75rem;
  color: var(--shell-nav-fg-muted);
}

.sidebar__action {
  display: grid;
  place-items: center;
  width: var(--shell-hit);
  height: var(--shell-hit);
  flex: none;
  padding: 0;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--shell-nav-fg-muted);
  cursor: pointer;
}

.sidebar__action:hover:not(:disabled) {
  background: var(--shell-nav-hover-bg);
  color: var(--p-primary-color);
}

.sidebar__action:focus-visible {
  outline: 2px solid var(--shell-nav-accent);
  outline-offset: -2px;
}

.sidebar__action:disabled {
  opacity: 0.5;
  cursor: progress;
}
</style>
