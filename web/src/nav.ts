/**
 * The sidebar's contents, as data.
 *
 * Sections that are not built yet are listed and disabled rather than hidden.
 * The people using this are replacing a Visual FoxPro menu they know by heart;
 * seeing where advances and payments will live — and that they are coming, not
 * missing — is worth more than a tidy list of two links. When a step lands,
 * `soon` comes off and `to` goes on, and nothing else changes — which is what
 * happened to Advances in step 5, Payments in step 6 and Interest in step 7.
 */
export interface NavItem {
  label: string
  icon: string
  /** The route to navigate to. Absent exactly when the screen does not exist yet. */
  to?: string
  /** Hidden from encoders. Mirrors the router guard, which mirrors the server. */
  manager?: boolean
  /** Rendered dead, with a marker. Implies `to` is absent. */
  soon?: boolean
}

export interface NavSection {
  /** Absent for the first group, which needs no heading above the brand. */
  label?: string
  items: NavItem[]
}

export const navigation: NavSection[] = [
  {
    items: [
      { label: 'Dashboard', icon: 'pi pi-home', to: '/' },
      { label: 'Customers', icon: 'pi pi-users', to: '/customers' },
    ],
  },
  {
    label: 'Transactions',
    items: [
      { label: 'Advances', icon: 'pi pi-wallet', to: '/advances' },
      { label: 'Payments', icon: 'pi pi-money-bill', to: '/payments' },
      { label: 'Interest', icon: 'pi pi-percentage', to: '/interest', manager: true },
    ],
  },
  {
    label: 'Insight',
    items: [
      { label: 'Reports', icon: 'pi pi-chart-bar', soon: true },
      // Step 6 built the per-customer statement, which is reached from the
      // customer rather than from here. This stays disabled until step 8 gives
      // it something of its own: statements across customers, and the print
      // layout that makes one worth handing over.
      { label: 'Statements', icon: 'pi pi-file', soon: true },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'Import customers', icon: 'pi pi-upload', to: '/customers/import', manager: true },
      { label: 'Users', icon: 'pi pi-id-card', manager: true, soon: true },
      { label: 'Audit log', icon: 'pi pi-history', manager: true, soon: true },
      { label: 'Settings', icon: 'pi pi-cog', manager: true, soon: true },
    ],
  },
]
