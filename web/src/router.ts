import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '@/stores/session'
import { resetPageTitle } from '@/pageTitle'

/**
 * What a route is allowed to say about itself.
 *
 * Declared rather than left to `unknown`, because the shell reads `title` and
 * `parent` on every navigation and a typo in one route would otherwise show up
 * as a blank bar rather than a compile error.
 */
declare module 'vue-router' {
  interface RouteMeta {
    /** Shown in the topbar, and the last crumb of the trail. */
    title?: string
    /** The screen this one hangs off, for the crumb before it. */
    parent?: { label: string; to: string }
    /** Reachable with no session. Setup and login, and nothing else. */
    public?: boolean
    /** Managers only, in the UI. The server enforces the real thing. */
    manager?: boolean
  }
}

// History mode, not hash: the Go server serves index.html for any unknown path,
// so deep links work and the URL stays clean on the counter PCs' app windows.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('@/pages/HomePage.vue'),
      meta: { title: 'Dashboard' },
    },
    {
      path: '/customers',
      name: 'customers',
      component: () => import('@/pages/CustomersPage.vue'),
      meta: { title: 'Customers' },
    },
    {
      path: '/customers/new',
      name: 'customer-new',
      component: () => import('@/pages/CustomerNewPage.vue'),
      meta: { title: 'New customer', parent: { label: 'Customers', to: '/customers' } },
    },
    {
      // Manager-only in the UI, and manager-only at the server too — this
      // guard only saves a pointless round trip, it does not enforce anything.
      path: '/customers/import',
      name: 'customers-import',
      component: () => import('@/pages/CustomerImportPage.vue'),
      meta: {
        title: 'Import customers',
        parent: { label: 'Customers', to: '/customers' },
        manager: true,
      },
    },
    {
      path: '/customers/:id(\\d+)',
      name: 'customer',
      component: () => import('@/pages/CustomerPage.vue'),
      meta: { title: 'Customer', parent: { label: 'Customers', to: '/customers' } },
    },
    {
      path: '/advances',
      name: 'advances',
      component: () => import('@/pages/AdvancesPage.vue'),
      meta: { title: 'Advances' },
    },
    {
      path: '/advances/new',
      name: 'advance-new',
      component: () => import('@/pages/AdvanceNewPage.vue'),
      meta: { title: 'New voucher', parent: { label: 'Advances', to: '/advances' } },
    },
    {
      path: '/advances/:id(\\d+)',
      name: 'advance',
      component: () => import('@/pages/AdvancePage.vue'),
      meta: { title: 'Voucher', parent: { label: 'Advances', to: '/advances' } },
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/pages/LoginPage.vue'),
      meta: { title: 'Sign in', public: true },
    },
    {
      path: '/setup',
      name: 'setup',
      component: () => import('@/pages/SetupPage.vue'),
      meta: { title: 'Set up Bokkie', public: true },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/pages/NotFoundPage.vue'),
      meta: { title: 'Not found' },
    },
  ],
})

/**
 * Decide, before each navigation, which of the three states this browser is in:
 * a fresh install that needs an administrator, a logged-out browser, or a
 * working session.
 *
 * This guard is a convenience, not a security boundary. Every API route
 * enforces its own access on the server, which is the only place it can be
 * enforced -- a determined person can navigate this SPA anywhere they like and
 * will simply find every request refused.
 */
router.beforeEach(async (to) => {
  const session = useSessionStore()

  // One call per page load. After that the store is the router's source of
  // truth, and the API's 401s are what correct it if a session dies mid-use.
  if (!session.loaded) {
    await session.load()
  }

  if (session.needsSetup) {
    return to.name === 'setup' ? true : { name: 'setup' }
  }

  // Setup is unreachable once an administrator exists, matching the server,
  // which answers 409 there for good.
  if (to.name === 'setup') {
    return { name: session.isLoggedIn ? 'home' : 'login' }
  }

  if (!session.isLoggedIn && !to.meta.public) {
    return { name: 'login' }
  }

  // Someone already signed in has no business on the login form.
  if (session.isLoggedIn && to.name === 'login') {
    return { name: 'home' }
  }

  // An encoder following a link to a manager's screen is sent somewhere useful
  // rather than shown a page whose every request comes back 403.
  if (to.meta.manager && !session.isManager) {
    return { name: 'customers' }
  }

  return true
})

// The name of whatever record we are leaving does not belong to where we are
// going, so it goes before the new screen has a chance to load its own.
router.afterEach((to) => resetPageTitle(to.meta.title))
