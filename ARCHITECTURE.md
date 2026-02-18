# OpenClaw Dashboard — Architecture Plan

> Single-file refactor. Zero dependencies. No build step.

## Current State (~750 lines JS)

**Problems:**
- 11 loose globals (`D`, `prevD`, `chartDays`, `uTab`, `srTab`, `stTab`, `prevUTab`, `prevSrTab`, `prevStTab`, `prevChartDays`, `timer`)
- `render()` is a 200+ line monolith mixing dirty-checking, DOM updates, and data transformation
- No separation between fetch, state, comparison, and rendering
- Inline `onclick` handlers reference globals directly
- Theme engine is separate but also uses loose globals (`THEMES`, `currentTheme`)

## Proposed Module Structure

Five plain JS objects inside a single `<script>` tag. No classes, no frameworks.

```
┌─────────────────────────────────────────────────┐
│                    App.init()                    │
│         (wires everything, starts timer)         │
└────────┬──────────────┬──────────────┬──────────┘
         │              │              │
    ┌────▼────┐   ┌─────▼─────┐  ┌────▼─────┐
    │  State  │◄──│ DataLayer │  │  Theme   │
    │ (truth) │   │  (fetch)  │  │ (colors) │
    └────┬────┘   └───────────┘  └──────────┘
         │
    ┌────▼────────────┐
    │  DirtyChecker   │
    │ (what changed?) │
    └────┬────────────┘
         │
    ┌────▼────┐
    │Renderer │
    │ (DOM)   │
    └─────────┘
```

### Data Flow

```
Timer tick / manual refresh
  → DataLayer.fetch()
  → State.update(newData)
  → DirtyChecker.diff(State.current, State.prev)
  → Renderer.render(dirtyFlags)
  → State.commitPrev()
```

---

## Module Breakdown

### 1. `State` — Single Source of Truth (~40 lines)

**Owns:**
- `data` — the fetched API response (currently `D`)
- `prev` — previous snapshot (currently `prevD`)
- `tabs` — `{ usage: 'today', subRuns: 'today', subTokens: 'today' }` (replaces `uTab`, `srTab`, `stTab`)
- `prevTabs` — `{ usage, subRuns, subTokens }` (replaces `prevUTab`, `prevSrTab`, `prevStTab`)
- `chartDays` — `7 | 30`
- `prevChartDays`
- `countdown` — seconds until next refresh

**Methods:**
| Method | Description |
|--------|-------------|
| `update(newData)` | Sets `data`, called after fetch |
| `setTab(group, value)` | Sets `tabs[group]` — e.g., `State.setTab('usage', '7d')` |
| `setChartDays(n)` | Sets `chartDays` |
| `commitPrev()` | Copies `data` → `prev`, `tabs` → `prevTabs`, `chartDays` → `prevChartDays` |
| `resetCountdown()` | Sets `countdown = 60` |
| `tick()` | Decrements `countdown`, returns `true` if hit 0 |

**Depends on:** Nothing.

### 2. `DataLayer` — Fetching & Refresh (~25 lines)

**Owns:** Nothing (stateless).

**Methods:**
| Method | Description |
|--------|-------------|
| `fetch()` | `GET /api/refresh?t=...`, returns parsed JSON |

**Depends on:** Nothing. Caller (`App`) writes result into `State`.

### 3. `DirtyChecker` — All Comparison Logic (~50 lines)

**Owns:** Nothing (pure functions).

**Methods:**
| Method | Description |
|--------|-------------|
| `sectionChanged(keys)` | Compares `State.data[key]` vs `State.prev[key]` via JSON stringify |
| `stableChanged(arrKey, fields)` | Like `sectionChanged` but uses `stableSnapshot()` — strips volatile timestamps |
| `tabChanged(group)` | `State.tabs[group] !== State.prevTabs[group]` |
| `chartDaysChanged()` | `State.chartDays !== State.prevChartDays` |
| `computeDirtyFlags()` | Returns `{ alerts, health, cost, crons, sessions, usage, subRuns, subTokens, charts, bottom }` — each boolean |

**Depends on:** `State` (reads `.data`, `.prev`, `.tabs`, `.prevTabs`).

**Migration note:** Move `sectionChanged()`, `stableSnapshot()`, and all the `if (!prevD || ...)` conditionals here. The inline checks in `render()` become calls to `DirtyChecker.computeDirtyFlags()` once at the top of `Renderer.render()`.

### 4. `Renderer` — All DOM Updates (~500 lines)

**Owns:** Nothing persistent. Pure DOM side-effects.

**Top-level method:**
| Method | Description |
|--------|-------------|
| `render(dirtyFlags)` | Dispatches to section renderers based on flags |

**Section renderers** (one function each):
| Function | DOM targets | Dirty flag |
|----------|------------|------------|
| `renderHeader()` | `#botName`, `#botEmoji`, `#statusDot`, `#statusText` | always |
| `renderAlerts()` | `#alertsSection` | `alerts` |
| `renderHealth()` | `#hGw`, `#hPid`, `#hUp`, `#hMem`, `#hComp`, `#hSess` | always (volatile) |
| `renderCost()` | `#cToday`, `#cAll`, `#cProj`, `#donut`, `#donutLegend` | `cost` |
| `renderCrons()` | `#cronBody`, `#cronCount` | `crons` |
| `renderSessions()` | `#sessBody`, `#sessCount`, `#agentTree` | `sessions` |
| `renderUsageTable()` | `#uBody` + tab buttons | `usage` |
| `renderSubRuns()` | `#srBody`, `#subCostLbl`, `#srEmpty` + tab buttons | `subRuns` |
| `renderSubTokens()` | `#stBody` + tab buttons | `subTokens` |
| `renderCharts()` | `#costChart`, `#modelChart`, `#subagentChart` + tab buttons | `charts` |
| `renderBottom()` | `#modelsGrid`, `#skillsGrid`, `#gitPanel`, all agent config panels | `bottom` |

**Sub-renderers within `renderBottom()`:**
- `renderAgentCards()`
- `renderModelRouting()`
- `renderRuntimeConfig()`
- `renderSearchPanel()`
- `renderGatewayPanel()`
- `renderHooksPanel()`
- `renderPluginsPanel()`
- `renderBindings()`
- `renderSubagentConfig()`
- `renderAgentTable()`

**Helper functions (stay in Renderer):**
- `renderTokenTbl(bodyId, data, accentColor)` — shared by usage + subTokens
- `renderAgentTree()` — called from `renderSessions()`
- `renderCostChart(id, data)`
- `renderModelChart(id, data)`
- `renderSubagentChart(id, data)`
- `setTabCls4(prefix, tab, cls)` — tab button class toggler

**Depends on:** `State` (reads `.data`, `.tabs`, `.chartDays`).

### 5. `Theme` — Theme Engine (~80 lines, mostly unchanged)

**Owns:**
- `themes` — loaded theme definitions (currently `THEMES`)
- `current` — current theme ID

**Methods:**
| Method | Description |
|--------|-------------|
| `load()` | Fetch `/themes.json`, apply saved theme |
| `apply(id)` | Set CSS variables, save to localStorage |
| `renderMenu()` | Populate `#themeMenu` |
| `toggle()` | Open/close menu |

**Depends on:** Nothing. Self-contained.

### 6. `App` — Initialization & Wiring (~40 lines)

**Owns:** The `setInterval` timer reference.

**Methods:**
| Method | Description |
|--------|-------------|
| `init()` | Called on load. Starts theme, first fetch, timer |
| `refresh()` | Manual refresh (button click) |
| `onTick()` | Decrement countdown, trigger fetch at 0 |

**Depends on:** All other modules.

---

## Utility Functions (top-level, ~20 lines)

Keep these as plain top-level functions (they're used everywhere):

- `$(id)` — `document.getElementById`
- `esc(s)` — HTML escape
- `safeColor(v)` — validate hex color
- `relTime(ts)` — relative timestamp
- `COLORS` — palette constant

---

## Inline `onclick` Handler Strategy

**Current:** `onclick="uTab='today';render()"` — references globals.

**After:** Expose a thin `UI` namespace on `window` for HTML bindings:

```js
// At the end of <script>, expose for inline handlers
window.UI = {
  setUsageTab:    v => { State.setTab('usage', v); App.renderNow(); },
  setSubRunsTab:  v => { State.setTab('subRuns', v); App.renderNow(); },
  setSubTokensTab:v => { State.setTab('subTokens', v); App.renderNow(); },
  setChartDays:   n => { State.setChartDays(n); App.renderNow(); },
  refresh:        () => App.refresh(),
  toggleTheme:    () => Theme.toggle(),
  applyTheme:     id => Theme.apply(id),
};
```

HTML becomes: `onclick="UI.setUsageTab('today')"` — clean, traceable, no globals.

---

## Migration Notes for Codex

### Step-by-step (do in order):

1. **Create `State` object.** Move `D` → `State.data`, `prevD` → `State.prev`, all tab variables → `State.tabs`, `timer` → `State.countdown`. Delete the old globals.

2. **Create `DataLayer` object.** Extract the fetch logic from `loadData()`. It should return data, not set globals.

3. **Create `DirtyChecker` object.** Move `sectionChanged()` and `stableSnapshot()` here. Add `computeDirtyFlags()` that returns all dirty booleans.

4. **Create `Renderer` object.** Split the monolithic `render()` into section functions. Each section function should:
   - Accept no arguments (reads from `State` directly)
   - Only be called when its dirty flag is true (except always-update sections like header/health)
   - The main `Renderer.render()` calls `DirtyChecker.computeDirtyFlags()` then dispatches

5. **Create `Theme` object.** Rename `THEMES` → `Theme.themes`, `currentTheme` → `Theme.current`. Move `loadThemes()`, `applyTheme()`, `renderThemeMenu()`, `toggleThemeMenu()`.

6. **Create `App` object.** Wire `init()` to call `Theme.load()`, `DataLayer.fetch()`, start `setInterval`. Wire `refresh()` for button.

7. **Create `window.UI` namespace.** Update all inline `onclick` handlers in HTML.

8. **Delete all loose globals** — nothing should remain outside the module objects except utilities (`$`, `esc`, `safeColor`, `relTime`, `COLORS`).

### Renames:
| Old | New |
|-----|-----|
| `D` | `State.data` |
| `prevD` | `State.prev` |
| `uTab` | `State.tabs.usage` |
| `srTab` | `State.tabs.subRuns` |
| `stTab` | `State.tabs.subTokens` |
| `prevUTab` | `State.prevTabs.usage` |
| `prevSrTab` | `State.prevTabs.subRuns` |
| `prevStTab` | `State.prevTabs.subTokens` |
| `chartDays` | `State.chartDays` |
| `prevChartDays` | `State.prevChartDays` |
| `timer` | `State.countdown` |
| `THEMES` | `Theme.themes` |
| `currentTheme` | `Theme.current` |
| `loadData()` | `App.refresh()` → `DataLayer.fetch()` |
| `render()` | `Renderer.render()` |
| `renderCharts()` | `Renderer.renderCharts()` |
| `renderAgentTree()` | `Renderer.renderAgentTree()` |

---

## Line Count Estimate

| Module | Lines |
|--------|-------|
| Utilities (`$`, `esc`, etc.) | ~20 |
| `State` | ~40 |
| `DataLayer` | ~25 |
| `DirtyChecker` | ~50 |
| `Theme` | ~80 |
| `Renderer` (all sections) | ~480 |
| `App` | ~35 |
| `window.UI` | ~15 |
| **Total JS** | **~745** |
| CSS (unchanged) | ~250 |
| HTML (unchanged) | ~200 |
| **Total file** | **~1195** |

Current total: ~1200 lines. Refactored: similar or slightly less.

---

## What Does NOT Change

- All CSS stays identical
- All HTML structure stays identical (only `onclick` values change)
- All visual behavior stays identical
- Theme engine logic stays the same (just reorganized)
- Chart SVG rendering stays the same (just moved into `Renderer`)
- `stableSnapshot` / dirty-check logic stays the same (just moved into `DirtyChecker`)
