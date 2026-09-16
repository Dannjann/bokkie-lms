import { ref } from 'vue'

/**
 * The name of the specific record on screen, when a route's static title is too
 * vague to be useful.
 *
 * "Customers › Customer" tells nobody which customer they are looking at. A
 * record screen calls setPageTitle once it knows; the router resets it on every
 * navigation, so a stale name can never outlive the page that set it.
 *
 * The window title follows the same value. The counter PCs run this in a
 * chromeless `--app` window with no tab strip, so the title is only ever read
 * in the taskbar — which is exactly where someone with three windows open needs
 * to tell them apart.
 */
const dynamic = ref('')

export const pageTitle = dynamic

function applyDocumentTitle(title: string): void {
  document.title = title ? `${title} — Bokkie` : 'Bokkie'
}

export function setPageTitle(title: string): void {
  dynamic.value = title
  applyDocumentTitle(title)
}

/** Called by the router on every navigation, with the new route's static title. */
export function resetPageTitle(routeTitle?: string): void {
  dynamic.value = ''
  applyDocumentTitle(routeTitle ?? '')
}
