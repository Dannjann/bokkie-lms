/**
 * The sidebar's contents, as data.
 *
 * Sections that are not built yet are listed and disabled rather than hidden.
 * The people using this are replacing a Visual FoxPro menu they know by heart;
 * seeing where advances and payments will live — and that they are coming, not
 * missing — is worth more than a tidy list of two links. When a step lands,
 * `soon` comes off and `to` goes on, and nothing else changes — which is what
 * happened to Advances in step 5, Payments in step 6 and Settlements in step 7.
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
      // Open to encoders: settling a farmer is counter work.
      { label: 'Liquidations', icon: 'pi pi-calculator', to: '/settlements' },
    ],
  },
  {
    label: 'Insight',
    items: [
      // Four reports rather than one "Reports" screen with a picker on it: each
      // answers a different question, and a manager who wants the aging does
      // not want to arrive somewhere else first and choose.
      { label: 'Current Advances', icon: 'pi pi-chart-bar', to: '/reports/current-advances', manager: true },
      { label: 'Return of Investment', icon: 'pi pi-percentage', to: '/reports/roi', manager: true },
      { label: 'Aging', icon: 'pi pi-hourglass', to: '/reports/aging', manager: true },
      { label: 'Liquidated', icon: 'pi pi-check-circle', to: '/reports/settled', manager: true },
      // An index over the per-customer statements, which are also still
      // reachable from the customer. Not a batch print: a statement is one
      // farmer's history and is printed from their own page. This entry carries
      // no `manager` flag, and that is deliberate — see handleListStatements.
      { label: 'Statements', icon: 'pi pi-file', to: '/statements' },
    ],
  },
  {
    label: 'Administration',
    items: [
      { label: 'Import customers', icon: 'pi pi-upload', to: '/customers/import', manager: true },
      { label: 'Users', icon: 'pi pi-id-card', to: '/users', manager: true },
      { label: 'Audit log', icon: 'pi pi-history', to: '/audit', manager: true },
      { label: 'Settings', icon: 'pi pi-cog', to: '/settings', manager: true },
    ],
  },
]
