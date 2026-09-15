# ADR-001 — Language and Runtime Choice

- **Status:** Accepted
- **Date:** 2026-09-15
- **Deciders:** Dan
- **Supersedes:** —
- **Related:** [00-architecture.md](00-architecture.md) § Decision 2

---

## Context

We are replacing a Visual FoxPro / DBF lending system at TLS Palay & Corn Buying
Station with a LAN-hosted web application (see `00-architecture.md` § Decision 1
for why hosted rather than standalone desktop).

Five constraints narrowed the field, listed by how much weight each carried:

1. **Non-technical operator, rural Isabela, no on-site IT support, ~10-year
   horizon.** If the host machine dies, someone has to get it running again —
   possibly not the original developer, possibly over the phone. *This is the
   dominant constraint.*
2. **Host OS is chosen at deploy time** — Windows, macOS, or Linux — so the
   station is not locked to one box or one OS licence.
3. **Raw ESC/P printing to a dot-matrix**, which needs OS-level spooler or CUPS
   access rather than anything a browser can do.
4. **One long-lived process** serving HTTP while also running a backup
   scheduler, an mDNS responder, and the print path. Not a request/response
   CGI model.
5. **5–10 concurrent users.** Performance is irrelevant; every candidate is
   comfortably fast enough. This constraint eliminated nothing.

Constraint 1 is the reason "single file, no runtime to install" outweighed "a
language the developer already knows."

### Non-goals

- Raw throughput or latency optimisation.
- Cloud deployment, multi-tenancy, or horizontal scaling.
- Mobile-native clients (deferred — see Alternatives § Flutter).

---

## Decision

**Go for the backend, Vue 3 + TypeScript + PrimeVue for the frontend, compiled
and embedded into a single static Go binary.**

Supporting choices that follow from it:

- `modernc.org/sqlite` — pure-Go SQLite, **no cgo**.
- `CGO_ENABLED=0` on every build target.
- `kardianos/service` for Windows SCM / launchd / systemd.
- `go:embed` for the compiled SPA.

---

## Consequences

### Positive

- **Deployment is "copy one file and run it."** No PHP, no Node, no web server,
  no runtime version to drift. This is the whole point.
- Five build targets cross-compile from a Mac with no C toolchain
  (win-amd64, mac-arm64, mac-amd64, linux-amd64, linux-arm64).
- Host OS becomes a reversible deploy-time choice rather than an architectural
  commitment, because SQLite files are byte-identical across platforms.
- Binary is ~15–25 MB and runs comfortably on a Raspberry Pi 5.
- Go's compatibility promise is unusually strong — important for a system
  expected to run for a decade with long stretches of no maintenance.
- Small language surface; a future maintainer can read it without knowing
  framework conventions.

### Negative

Stated plainly, because these are real:

- **The developer does not know Go.** A genuine tax on the first weeks.
- **No Filament equivalent.** Every CRUD screen is hand-written. This is the
  single largest velocity loss versus the Laravel alternative, and this app is
  mostly CRUD.
- **Verbose** — expect roughly 2–3× the line count of an equivalent Laravel
  implementation for the same screens.
- **Weaker reporting/PDF ecosystem** than PHP or .NET.
- **Still a two-language project** (Go + TypeScript). Go does not win the
  "one language everywhere" argument that Blazor would have.

### Neutral

- `sqlc` + `goose` means writing real SQL rather than using an ORM. A trade,
  not a cost: more explicit, less magic, more typing.

### Load-bearing constraint

> **The pure-Go SQLite driver is not a preference.** Swapping to a cgo driver
> (e.g. `mattn/go-sqlite3`) breaks cross-compilation for every non-host target
> at once and silently ends the host-OS flexibility above. The CI matrix in
> task 1.1 exists specifically to catch this, and the constraint belongs in a
> code comment beside the import.

---

## Alternatives considered

### .NET 8/9 (C#) — closest rival

| Pros | Cons |
|---|---|
| Self-contained publish gives a single executable | Binary is 70–100 MB (3–5× Go) |
| **Best-in-class Windows printing** — it's Microsoft's own platform | Cross-compiling to `linux-arm64` is clunkier |
| QuestPDF is excellent and free at this revenue scale | Native AOT fights EF Core reflection unless using compiled models |
| EF Core migrations more batteries-included than sqlc + goose | Also a new language for the developer |
| **Blazor Server would eliminate the second language entirely** | Blazor holds a SignalR connection per client — fine on a LAN, but another failure mode |

**Genuinely close.** The deciding factors were binary size, ARM cross-compilation
for a possible Pi appliance, and that Go's `CGO_ENABLED=0` story is simply
cleaner. **Had the host been committed to Windows, this would rate at least
even with Go**, and Blazor's single-language stack might have won it.

### Node.js / TypeScript

| Pros | Cons |
|---|---|
| **Closest to the developer's existing skills** (Vue/Nuxt/TS) | **Single-binary story is poor**: `vercel/pkg` is archived, `node --experimental-sea` is still rough, Bun's `--compile` is less proven on Windows |
| One language across frontend and backend | Otherwise: install Node on the host, or ship ~100 MB |
| `node:sqlite` is now built in — the old `better-sqlite3` native-module problem is largely gone | Raw printing needs a native addon or shelling out to `lp` |
| Huge ecosystem; Fastify/Hono/NestJS all mature | npm dependency churn versus a system meant to sit untouched for years |

Viable, and the skills argument is real. Packaging is what disqualified it
against constraint 1.

### PHP / Laravel + Filament

| Pros | Cons |
|---|---|
| **The developer's strongest skill** | **Deployment on a rural Windows PC is the core problem** — needs a PHP runtime plus a web server |
| **Filament is precisely suited to this app** — customer master, line-item grids, payments, reports. Plausibly **3–5× faster to build** | `frankenphp build-static-app` does produce a standalone binary, but Windows support is newer and less proven — and Windows is the most likely host |
| Mature PDF and reporting ecosystem | FPM's request/response model doesn't fit the always-on scheduler + mDNS + print daemon; needs a separate worker |
| Existing be-kit tooling in place | Raw dot-matrix printing from PHP on Windows is awkward |

**The honest temptation, and the strongest counter-argument to this ADR.** If
the dominant project risk turns out to be *never finishing*, rather than
*deployment fragility*, this becomes the rational choice.

### Rust

Single binary, excellent cross-compilation, strongest safety guarantees — and
by far the steepest learning curve and slowest CRUD development. Unjustified
for a 10-screen internal app with no performance requirement. **Rejected.**

### Java / Kotlin

Mature and widely known, and GraalVM native-image can produce a single binary —
but reflection makes native-image painful, and the alternative is a JVM install
or a large bundle. Poor fit for constraint 1. **Rejected.**

### Flutter / Dart *(the original proposal)*

Rejected as the *first* layer, not rejected outright. It solves only the client
half — multiple PCs and phones sharing data require a server process regardless,
so choosing Flutter adds a second codebase rather than removing one. The common
multi-user shortcut (a shared SQLite file on a network drive) corrupts under
concurrent writes, which would reproduce the legacy system's central flaw.

**A Flutter Android client for field collectors remains viable later**, against
the same JSON API, with no backend changes. That is a better moment to spend the
effort — when offline-first mobile actually earns it.

---

## Revisit triggers

Reopen this decision if any of the following becomes true:

| Trigger | Likely new choice |
|---|---|
| Host is committed to Windows permanently | **.NET** — equal or better, and Blazor removes the second language |
| Delivery risk dominates; the project is stalling | **Laravel + Filament** — shipped beats elegant |
| The developer will always be the sole, reachable maintainer | **Node/TS** becomes defensible; deployment pain lands on them, not the station |
| A Pi/mini-PC appliance is chosen as the host | **Stay with Go** — smallest footprint, cleanest ARM story |

## The assumption this rests on

**Deployment simplicity matters more than development speed**, because the
system outlives the developer's attention and the people operating it cannot
debug a broken runtime.

If that assumption is wrong, this ADR is wrong, and Laravel + Filament is the
better call.
