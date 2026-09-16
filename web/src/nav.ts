/**
 * The sidebar's contents, as data.
 *
 * Sections that are not built yet are listed and disabled rather than hidden.
 * The people using this are replacing a Visual FoxPro menu they know by heart;
 * seeing where advances and payments will live — and that they are coming, not
 * missing — is worth more than a tidy list of two links. When step 5 lands,
 * `soon` comes off and `to` goes on, and nothing else changes.
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
      { label: 'Payments', icon: 'pi pi-money-bill', soon: true },
      { label: 'Interest', icon: 'pi pi-percentage', soon: true },
    ],
  },
  {
    label: 'Insight',
    items: [
      { label: 'Reports', icon: 'pi pi-chart-bar', soon: true },
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
