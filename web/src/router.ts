import { createRouter, createWebHistory } from 'vue-router'
import { useSessionStore } from '@/stores/session'

// History mode, not hash: the Go server serves index.html for any unknown path,
// so deep links work and the URL stays clean on the counter PCs' app windows.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'home',
      component: () => import('@/pages/HomePage.vue'),
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/pages/LoginPage.vue'),
      meta: { public: true },
    },
    {
      path: '/setup',
      name: 'setup',
      component: () => import('@/pages/SetupPage.vue'),
      meta: { public: true },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/pages/NotFoundPage.vue'),
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

  return true
})
