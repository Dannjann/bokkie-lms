import { definePreset } from '@primeuix/themes'
import Aura from '@primeuix/themes/aura'

/**
 * Bokkie's palette, as a PrimeVue preset.
 *
 * Teal rather than Aura's default emerald: this is a lending ledger, and a
 * green that reads as "money in" next to a row of peso figures is a
 * distraction. Teal is close enough to feel agricultural and far enough from
 * the success/danger colours that nobody mistakes an accent for a status.
 *
 * Defined here rather than inline in main.ts because the shell chrome in
 * styles/app.css has to match it, and one file should be the place you change
 * the colour.
 */
export const BokkiePreset = definePreset(Aura, {
  semantic: {
    primary: {
      50: '#f0fdfa',
      100: '#ccfbf1',
      200: '#99f6e4',
      300: '#5eead4',
      400: '#2dd4bf',
      500: '#14b8a6',
      600: '#0d9488',
      700: '#0f766e',
      800: '#115e59',
      900: '#134e4a',
      950: '#042f2e',
    },
    colorScheme: {
      light: {
        primary: {
          color: '{primary.600}',
          contrastColor: '#ffffff',
          hoverColor: '{primary.700}',
          activeColor: '{primary.800}',
        },
      },
    },
  },
})
