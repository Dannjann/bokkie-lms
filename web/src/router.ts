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
    /**
     * The station's own screens: about the machine, not the books. Outside
     * every guard, because the window opens on one before anybody has logged
     * in and the server refuses them from any machine but the host anyway.
     */
    station?: boolean
  }
}

// History mode, not hash: the Go server serves index.html for any unknown path,
// so deep links work and the URL stays clean on the counter PCs' app windows.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    // The station's own screens. Public, because the person who opens the
    // window has not logged in and should not have to in order to see whether
    // the station is running — and the server refuses these from any machine
    // but the host regardless of what the browser believes.
    {
      path: '/station',
      name: 'station',
      component: () => import('@/pages/StationPage.vue'),
      meta: { title: 'Station', public: true, station: true },
    },
    {
      path: '/station/check',
      name: 'station-check',
      component: () => import('@/pages/StationCheckPage.vue'),
      meta: { title: 'Check', public: true, station: true },
    },
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
      path: '/payments',
      name: 'payments',
      component: () => import('@/pages/PaymentsPage.vue'),
      meta: { title: 'Payments' },
    },
    {
      path: '/payments/new',
      name: 'payment-new',
      component: () => import('@/pages/PaymentNewPage.vue'),
      meta: { title: 'New payment', parent: { label: 'Payments', to: '/payments' } },
    },
    {
      path: '/payments/:id(\\d+)',
      name: 'payment',
      component: () => import('@/pages/PaymentPage.vue'),
      meta: { title: 'Payment', parent: { label: 'Payments', to: '/payments' } },
    },
    {
      // Manager-only in the UI and at the server, reads included — the whole
      // section is, and this guard only saves a pointless round trip.
      path: '/interest',
      name: 'interest',
      component: () => import('@/pages/InterestPage.vue'),
      meta: { title: 'Interest', manager: true },
    },
    {
      path: '/interest/new',
      name: 'interest-new',
      component: () => import('@/pages/InterestNewPage.vue'),
      meta: {
        title: 'New interest run',
        parent: { label: 'Interest', to: '/interest' },
        manager: true,
      },
    },
    {
      path: '/interest/:id(\\d+)',
      name: 'interest-run',
      component: () => import('@/pages/InterestRunPage.vue'),
      meta: {
        title: 'Interest run',
        parent: { label: 'Interest', to: '/interest' },
        manager: true,
      },
    },
    {
      // The four reports. Manager-only in the UI and at the server, reads
      // included: they are portfolio views, and what an encoder needs about one
      // farmer is on the customer screen and the statement.
      path: '/reports/current-advances',
      name: 'report-current-advances',
      component: () => import('@/pages/CurrentAdvancesPage.vue'),
      meta: {
        title: 'Current Advances',
        parent: { label: 'Reports', to: '/reports/current-advances' },
        manager: true,
      },
    },
    {
      path: '/reports/roi',
      name: 'report-roi',
      component: () => import('@/pages/ROIPage.vue'),
      meta: {
        title: 'Return of Investment',
        parent: { label: 'Reports', to: '/reports/current-advances' },
        manager: true,
      },
    },
    {
      path: '/reports/aging',
      name: 'report-aging',
      component: () => import('@/pages/AgingPage.vue'),
      meta: {
        title: 'Aging',
        parent: { label: 'Reports', to: '/reports/current-advances' },
        manager: true,
      },
    },
    {
      path: '/reports/liquidated',
      name: 'report-liquidated',
      component: () => import('@/pages/LiquidatedPage.vue'),
      meta: {
        title: 'Liquidated',
        parent: { label: 'Reports', to: '/reports/current-advances' },
        manager: true,
      },
    },
    {
      // The index over every farmer's statement. Not manager-gated, unlike the
      // four reports — the encoder at the counter is the person who has to tell
      // a farmer what they owe.
      path: '/statements',
      name: 'statements',
      component: () => import('@/pages/StatementsPage.vue'),
      meta: { title: 'Statements' },
    },
    {
      // Hangs off the customer as well as off the index: this is one farmer's
      // history and both ways in are legitimate. The crumb points back to
      // Customers because that is where the record lives.
      path: '/customers/:id(\\d+)/statement',
      name: 'statement',
      component: () => import('@/pages/StatementPage.vue'),
      meta: { title: 'Statement', parent: { label: 'Customers', to: '/customers' } },
    },
    {
      // Manager-only in the UI and at the server. The roster is a management
      // question, and an encoder has no use for it.
      path: '/users',
      name: 'users',
      component: () => import('@/pages/UsersPage.vue'),
      meta: { title: 'Users', manager: true },
    },
    {
      // Manager-only. The log records who did what to the money, and an encoder
      // reading their colleagues' movements is not what it is for.
      path: '/audit',
      name: 'audit',
      component: () => import('@/pages/AuditPage.vue'),
      meta: { title: 'Audit log', manager: true },
    },
    {
      // Manager-only to change, and manager-only to look at: the Ref. No. start
      // and the interest rule are not things to be browsed mid-shift.
      path: '/settings',
      name: 'settings',
      component: () => import('@/pages/SettingsPage.vue'),
      meta: { title: 'Settings', manager: true },
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

  // The station's own screens are outside all of this. They are about the
  // machine rather than the books: whether it is serving, at what address, when
  // it last backed up, and how to stop it.
  //
  // Exempting them from the setup redirect is not a convenience. The window
  // opens on /station in application mode, with no address bar — so on a brand
  // new station the redirect to /setup would take the Stop button, the address
  // and the backup time away for the whole of the first session, which is the
  // session somebody is watching.
  if (to.meta.station) {
    return true
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
router.afterEach((to) => {
  resetPageTitle(to.meta.title)
  forgetReload()
})

/**
 * Every route above loads its screen with a dynamic import, so each one is its
 * own file with a content hash in its name. When the server starts serving a
 * new build, those names change — and a browser that has been open since before
 * the update asks for a file that is no longer there.
 *
 * **This is not a rare case here, it is what `bokkie update` does.** The
 * counter PCs run the app in a chromeless window that nobody closes for weeks;
 * a USB update swaps the binary underneath them, and the next screen any of
 * them opens is a chunk that no longer exists. Untreated, the import rejects
 * and vue-router simply stops: on a click the navigation aborts and the screen
 * that is already there stays, and on a refresh or a typed address there is no
 * screen to stay on, so the rail and the topbar render around an empty middle.
 * That blank middle is what somebody rings up about.
 *
 * The page that would work is the one the server is serving right now, so the
 * answer is to go and get it. A full load — not a router navigation — fetches
 * a fresh index.html naming the files that do exist.
 */
router.onError((error, to) => {
  if (!isStaleChunk(error)) return

  // Once per destination, and no more. A chunk that is genuinely missing —
  // rather than merely renamed — would otherwise turn into a browser that
  // reloads itself for ever, which is worse than a blank screen because
  // nobody can read the address bar to tell you where they are.
  if (alreadyReloadedFor(to.fullPath)) return
  rememberReload(to.fullPath)

  // assign rather than reload: on a click the address bar still holds the page
  // being left, and reloading that would land the person somewhere they did
  // not ask for.
  window.location.assign(to.fullPath)
})

/**
 * Whether this failure is a screen that could not be fetched.
 *
 * Matched on the message because that is all there is: the three engines throw
 * a plain TypeError with no code on it, and each words it differently. Chromium
 * says "Failed to fetch dynamically imported module", Firefox "error loading
 * dynamically imported module", Safari "Importing a module script failed".
 */
function isStaleChunk(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error)
  return (
    /dynamically imported module/i.test(message) ||
    /Importing a module script failed/i.test(message) ||
    /Unable to preload CSS/i.test(message)
  )
}

const RELOAD_KEY = 'bokkie:reloaded-for'

// sessionStorage rather than a module variable: the whole point is to survive
// the reload we are about to do, and to be forgotten when the tab closes.
// Wrapped because a browser with storage disabled must degrade to the blank
// screen we have today rather than to an exception thrown inside an error
// handler.
function alreadyReloadedFor(path: string): boolean {
  try {
    return sessionStorage.getItem(RELOAD_KEY) === path
  } catch {
    return false
  }
}

function rememberReload(path: string): void {
  try {
    sessionStorage.setItem(RELOAD_KEY, path)
  } catch {
    // Nothing to do: without storage the guard above returns false and the
    // reload simply is not deduplicated.
  }
}

// A navigation that arrives is proof the reload worked, so the next stale chunk
// — after the next update — gets its own attempt.
function forgetReload(): void {
  try {
    sessionStorage.removeItem(RELOAD_KEY)
  } catch {
    // As above.
  }
}
