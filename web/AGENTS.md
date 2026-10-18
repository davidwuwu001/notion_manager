# notion-manager Dashboard — Frontend Development Guide

## Tech Stack

- **Framework**: React 19 + TypeScript
- **Build**: Vite 6
- **Styling**: TailwindCSS v4 (via `@tailwindcss/vite` plugin)
- **Embedding**: Go `//go:embed` — compiled dist is embedded into the Go binary

## Project Structure

```
web/
├── index.html          # HTML entry point
├── package.json        # Dependencies
├── vite.config.ts      # Vite config (base: /dashboard/, proxy to :8081)
├── tsconfig.json       # TypeScript config
└── src/
    ├── main.tsx        # React root mount
    ├── App.tsx         # Auth, six-page hash navigation, account pool
    ├── api.ts          # API calls (/admin/accounts, /proxy/start)
    ├── types.ts        # TypeScript interfaces (AccountInfo, DashboardData, Model)
    ├── utils.ts        # Helpers (avatarColor, quota status, formatting)
    ├── dashboard.ts    # Navigation, filter and nullable metric helpers
    ├── components/     # Overview, logs, analytics, settings and models
    ├── index.css       # TailwindCSS imports + theme variables
    └── vite-env.d.ts   # Vite type reference
```

## Build & Deploy

```bash
# Development (hot reload, proxies API to localhost:8081)
cd web && npm run dev

# Portable frontend build (does not replace the running service)
npm run build

# Windows build helpers
make web          # uses Windows cmd/xcopy to copy into internal/web/dist/
make build        # builds frontend + Go binary
make build-go     # Go binary only (uses existing internal/web/dist/)
```

The build pipeline:
1. `npm run build` → `web/dist/`
2. `xcopy web/dist internal/web/dist/` (embedded into Go binary)
3. `go build` with `//go:embed dist/*` in `internal/web/embed.go`
4. Go serves at `/dashboard/`; management calls use the dashboard session.
5. Public model IDs come from `/admin/models.public_models`, matching `/v1/models`.
6. The API key is read only after an explicit show/copy action via the protected
   `/admin/connection` endpoint. Never read credentials from HTML metadata.

On macOS/Linux, use the project release workflow for the embed copy and Go
build; the Windows Makefile copy commands are not portable. A Vite build alone
does not update the embedded or running app.

## Pages and New Management Contracts

Use `/dashboard/#overview`, `#accounts` (default), `#models`, `#logs`,
`#analytics`, `#settings`; no router dependency is needed. Each new panel owns
its loading/error state and aborts reads when unmounted. A 401 expires the
dashboard session. Never display failed reads as successful zero metrics.

- `/admin/request-logs`: stable cursor pagination; metadata filters only.
- `/admin/request-logs/{id}`: direct request and attempt lookup.
- `/admin/analytics`: request counts, successful-request latency and coverage.
- `/admin/routing`: quota/round_robin, enabled pool members, attempt budget,
  per-account concurrency and request-log retention. No primary/backup roles.
- `/admin/stats`: original historical Token totals, kept independent.

Unknown metrics use `—`. Explain collection/retention coverage separately from
storage degradation. Account distribution counts participating requests per
account and attempt outcomes; rows must not be summed as global requests.
Only verified non-trial Business workspaces may be added. Preserve Chrome,
JSON and token import; old registration history is read-only.

Pure logic and management-contract checks use the existing Node test runner:
`node --test web/src/*.test.mjs` from the repository root. Type-check and build
with `npm --prefix web run build`; verify the actual Go-served app separately.

## Design System

### Theme (Notion Dark)

All theme colors are defined as CSS custom properties in `src/index.css` under `@theme`:

| Token              | Value                    | Usage                     |
|--------------------|--------------------------|---------------------------|
| `--color-bg-primary`   | `#191919`            | Page background           |
| `--color-bg-secondary` | `#202020`            | Header background         |
| `--color-bg-card`      | `#252525`            | Card background           |
| `--color-bg-card-hover`| `#2f2f2f`            | Card hover state          |
| `--color-bg-exhausted` | `#2a1f1f`            | Exhausted account card bg |
| `--color-text-primary` | `#ebebea`            | Primary text              |
| `--color-text-secondary`| `#9b9a97`           | Secondary/label text      |
| `--color-text-muted`   | `#5a5a5a`            | Muted/timestamp text      |
| `--color-notion-blue`  | `#2383e2`            | Accent / links / buttons  |
| `--color-ok`           | `#4dab9a`            | Available / healthy       |
| `--color-warn`         | `#d9a651`            | Low quota warning         |
| `--color-err`          | `#eb5757`            | Exhausted / error         |
| `--color-research`     | `#b39ddb`            | Research mode badge       |

Use TailwindCSS utility classes with these tokens, e.g. `bg-bg-card`, `text-text-primary`, `text-ok`.

### Typography

- Font: system font stack (`-apple-system, BlinkMacSystemFont, "Segoe UI", ...`)
- Summary stat values: `text-2xl font-bold tabular-nums`
- Card title: `text-[13px] font-semibold`
- Labels: `text-[11px] text-text-secondary uppercase tracking-wider`
- Timestamps: `text-[10px] text-text-muted`

### Components

- **StatCard**: Summary metric with label, value, sub-text
- **TotalQuotaBar**: Full-width progress bar with color-coded fill
- **AccountCard**: Clickable card with avatar, badges, quota bar, model pills
- **Badge**: Small pill with variant styling (`paid`, `free`, `research`, `exhausted`, `ok`, `model`)
- **QuotaBar**: Thin progress bar with color based on usage percentage

### Quota Display Convention

**Important**: Quota bars and numbers show **已使用 / 总额度** (used / total) format.
- The progress bar fill represents **usage** (how much has been consumed)
- Color coding: green (<70%), yellow (70-90%), red (>90%)
- Do NOT label as "配额" alone — it's ambiguous. Use "已使用" or "used / total" phrasing
- The summary card shows **剩余** (remaining) which is `total - used`

### Account Status

| Status    | Dot Color | Card Style              | Badge       |
|-----------|-----------|-------------------------|-------------|
| Available | Green     | Default bg              | `✓ 可用`    |
| Low quota | Yellow    | Default bg              | `✓ 可用`    |
| Exhausted | Red       | `bg-bg-exhausted`       | `⛔ 耗尽`   |
| Permanent | Red       | `bg-bg-exhausted` + 55% opacity | `⛔ 耗尽` |

### Research Mode

Research mode is available on **paid plans only** (`plus`, `business`, `enterprise`, `team`).
- Show `🔬 Research` badge on paid plan accounts
- Research mode uses the same AI quota but consumes significantly more per query (7+ search rounds, 3-5 min per query)
- Research mode uses request type `"researcher"` instead of `"workflow"`

## API Integration

### Data Source

The dashboard fetches from `GET /admin/accounts` with the dashboard
session cookie. New management APIs never use a public API key.

Pagination params (all optional): `?q=<substr>&page=<n>&page_size=<n>`.
- Without any params, the response is the full unsorted list (legacy
  shape; kept for scripts/integrations).
- With any param the server filters by `q` (matches email/name/plan/space,
  case-insensitive), sorts by health (healthy/most-remaining first,
  exhausted/no-workspace/permanent to the bottom), and slices to the
  requested page. `page_size` is clamped to `[1, 500]` (default 50).
- The pool-wide `summary` block is always returned and is computed
  across ALL accounts regardless of `q` — that way the headline cards
  stay stable while the user searches.

Response shape:
```json
{
  "total": 247,
  "available": 130,
  "filtered_total": 12,
  "page": 0,
  "page_size": 20,
  "models": [{ "id": "...", "name": "..." }],
  "summary": {
    "exhausted_only": 4,
    "no_workspace": 2,
    "premium_accounts": 5,
    "research_limited": 6,
    "total_research_usage": 14,
    "total_remaining": 12345,
    "total_space_usage": 1000, "total_space_limit": 4000,
    "total_user_usage": 1100, "total_user_limit": 4000,
    "total_space_remaining": 3000, "total_user_remaining": 2900,
    "total_premium_balance": 500, "total_premium_limit": 1000
  },
  "accounts": [{
    "email": "...",
    "name": "...",
    "plan": "personal",
    "space": "...",
    "exhausted": false,
    "permanent": false,
    "eligible": true,
    "usage": 103,
    "limit": 200,
    "remaining": 97,
    "checked_at": "2026-03-17T...",
    "models": [{ "id": "...", "name": "..." }]
  }]
}
```

### Proxy Navigation

- Click account card → `GET /proxy/start?account_id=<id>` (opens in new tab)
- "Open Best" button → `GET /proxy/start?best=true`
- Both create a `np_session` cookie and redirect to `/ai` (Notion reverse proxy)

## Conventions

- Keep the existing account view in `App.tsx`; new page components live in `components/`.
- No external UI library — TailwindCSS utility classes only
- Responsive: 5-col summary grid → 2-col on tablet → 1-col on mobile
- Account cards sorted server-side: healthy (most remaining first), then
  research-limited, then exhausted, then no-workspace, then permanent
- Search is debounced 250ms and forwarded to the server as `?q=`; the
  server filters across all accounts on email/name/plan/workspace
- Pagination: 20 cards per page, fetched on demand (`?page=&page_size=`)
- Keyboard shortcut: `/` focuses search, `Escape` blurs
