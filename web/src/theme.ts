import { definePreset } from '@primeuix/themes'
import Aura from '@primeuix/themes/aura'

/**
 * Bokkie's palette, as a PrimeVue preset.
 *
 * A light blue rather than Aura's default emerald. This is a lending ledger,
 * and a green that reads as "money in" next to a row of peso figures is a
 * distraction; blue is the colour the platform itself uses for "this is
 * something you can press", so an accent never gets mistaken for a status.
 *
 * The scale is tuned so the shade that carries white text — primary.color,
 * 600 — clears WCAG AA (5.1:1) for a 14px button label. The lighter steps are
 * where the "light blue" lives: the rail, the selected row, the hover wash.
 *
 * Defined here rather than inline in main.ts because the shell chrome in
 * styles/app.css has to match it, and one file should be the place you change
 * the colour.
 */
export const BokkiePreset = definePreset(Aura, {
  primitive: {
    // Slightly rounder than Aura's 6px, closer to the platform controls the
    // staff see everywhere else on the machine.
    borderRadius: {
      none: '0',
      xs: '3px',
      sm: '5px',
      md: '8px',
      lg: '10px',
      xl: '14px',
    },
  },
  semantic: {
    primary: {
      50: '#f0f7ff',
      100: '#e0effe',
      200: '#b9dffd',
      300: '#7cc5fb',
      400: '#36a7f6',
      500: '#0c8ce6',
      600: '#0270c4',
      700: '#035a9f',
      800: '#074c83',
      900: '#0c406d',
      950: '#082949',
    },
    colorScheme: {
      light: {
        primary: {
          color: '{primary.600}',
          contrastColor: '#ffffff',
          hoverColor: '{primary.700}',
          activeColor: '{primary.800}',
        },
        highlight: {
          background: '{primary.50}',
          focusBackground: '{primary.100}',
          color: '{primary.700}',
          focusColor: '{primary.800}',
        },
      },
    },
  },
})
