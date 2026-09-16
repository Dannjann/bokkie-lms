import { createApp } from 'vue'
import { createPinia } from 'pinia'
import PrimeVue from 'primevue/config'
import 'primeicons/primeicons.css'

import App from '@/App.vue'
import { router } from '@/router'
import { BokkiePreset } from '@/theme'
import '@/styles/app.css'

createApp(App)
  .use(createPinia())
  .use(router)
  .use(PrimeVue, {
    theme: {
      preset: BokkiePreset,
      options: {
        // No dark mode, ever. See the note on color-scheme in styles/app.css --
        // a browser set to dark must not turn a ledger grey.
        darkModeSelector: false,
      },
    },
    // The station runs on a LAN with no internet, and staff use this all day in
    // one language: no locale loading, no CDN fonts, nothing that can fail
    // because the connection is down.
    ripple: false,
  })
  .mount('#app')
