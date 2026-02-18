# TODO

## ✅ Released

- **v2.7.0** — Security hardening & performance fixes (XSS, CORS, O(N²), shell safety, file handles)
- **v2.8.0** — Performance, dirty-checking & test suite (44 ACs, rAF, scroll preserve, tab fix)

---

## 🏗️ Architecture Refactor (v2.9.0)

Clean module structure — single file, zero deps. Opus designed, Codex reviewed.
See `ARCHITECTURE.md` for full spec.

Before implementing, apply these design tweaks (from Codex review):

- [ ] App owns `computeDirtyFlags()` — not Renderer (fix flow contract contradiction in doc)
- [ ] Rename `window.UI` → `window.OCUI` (avoid global namespace collision)
- [ ] Immutable snapshot per render cycle — `const snap = State.snapshot()` passed to both DirtyChecker and Renderer
- [ ] Split `bottom` dirty flag into 4 granular flags: `models`, `skills`, `git`, `agentConfig`
- [ ] Document non-functional guarantees in ARCHITECTURE.md: scroll preservation, rAF batching, error handling, out-of-order fetch protection
- [ ] Update ATDD tests AC17–AC20 to new architecture names after refactor (`prevD` → `State.prev`, `loadData` → `App.refresh`, etc.)

## 🐳 Deployment (v2.10.0)

- [ ] **Dockerfile** — containerized dashboard: Python slim image, copy `index.html` + `server.py` + `refresh.sh` + `themes.json`, expose port 8080, mount openclaw config as volume
- [ ] **Nix flake** — `flake.nix` with `devShell` (python3 + bash deps) and `packages.default` for reproducible installs on NixOS / nix-darwin

## ⚡ Performance (v2.9.1 post-architecture)

- [ ] Volatile timestamp fix — `stableSnapshot()` for sessions/crons/subagentRuns dirty-checks (exclude `lastRun`, `nextRun`, `timestamp`, `updatedAt`)
- [ ] DOM/SVG incremental updates — Option B keyed row reconciliation + Option C SVG attr updates (only if refresh < 10s or tables > 100 rows)

## 🧪 Tests

- [ ] Update static tests AC17–AC20 after architecture refactor (regex patterns reference old global names)
- [ ] Add Playwright E2E tests for tab switching, chart toggle, auto-refresh cycle (optional, needs `playwright` dep in venv)

## 📦 Release Plan

- [ ] **v2.9.0** — Architecture refactor (State/DataLayer/DirtyChecker/Renderer/Theme modules)
- [ ] **v2.9.1** — Volatile timestamp stableSnapshot fix
- [ ] **v2.10.0** — Dockerfile + Nix flake

## 🔖 Notes

- 44/44 tests passing (`test_frontend.py` + `test_data_schema.py` + `test_server.py` + `test_critical.py`)
- Architecture doc: `ARCHITECTURE.md`
- Test runner: `.venv/bin/pytest tests/ -v`
