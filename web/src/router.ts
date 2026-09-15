import { createRouter, createWebHistory } from 'vue-router'

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
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/pages/NotFoundPage.vue'),
    },
  ],
})
