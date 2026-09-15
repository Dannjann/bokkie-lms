import { createApp } from 'vue'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import Aura from '@primeuix/themes/aura'
import 'primeicons/primeicons.css'

import App from '@/App.vue'
import { router } from '@/router'

createApp(App)
  .use(createPinia())
  .use(router)
  .use(PrimeVue, {
    theme: { preset: Aura },
    // The station runs on a LAN with no internet, and staff use this all day in
    // one language: no locale loading, no CDN fonts, nothing that can fail
    // because the connection is down.
    ripple: false,
  })
  .mount('#app')
