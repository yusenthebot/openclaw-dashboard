# TODO

## ✅ Released

- **v2026.2.18** — Security hardening (XSS, CORS, O(N²), shell safety, file handles)
- **v2026.2.19** — Performance, dirty-checking & test suite (44 ACs, rAF, scroll preserve, tab fix)

---

## 🏗️ Architecture Refactor (v2026.2.XX)

Clean module structure — single file, zero deps. Opus designed, Codex reviewed.
See `ARCHITECTURE.md` for full spec.

Before implementing, apply these design tweaks (from Codex review):

- [ ] App owns `computeDirtyFlags()` — not Renderer (fix flow contract contradiction in doc)
- [ ] Rename `window.UI` → `window.OCUI` (avoid global namespace collision)
- [ ] Immutable snapshot per render cycle — `const snap = State.snapshot()` passed to both DirtyChecker and Renderer
- [ ] Split `bottom` dirty flag into 4 granular flags: `models`, `skills`, `git`, `agentConfig`
- [ ] Document non-functional guarantees in ARCHITECTURE.md: scroll preservation, rAF batching, error handling, out-of-order fetch protection
- [ ] Update ATDD tests AC17–AC20 to new architecture names after refactor (`prevD` → `State.prev`, `loadData` → `App.refresh`, etc.)

## ⚡ Performance (v2026.2.XX)

- [ ] Volatile timestamp fix — `stableSnapshot()` for sessions/crons/subagentRuns dirty-checks (exclude `lastRun`, `nextRun`, `timestamp`, `updatedAt`)
- [ ] DOM/SVG incremental updates — Option B keyed row reconciliation + Option C SVG attr updates (only if refresh < 10s or tables > 100 rows)

## 🐳 Deployment (v2026.2.XX)

- [ ] **Dockerfile** — containerized dashboard: Python slim image, copy `index.html` + `server.py` + `refresh.sh` + `themes.json`, expose port 8080, mount openclaw config as volume
- [ ] **Nix flake** — `flake.nix` with `devShell` (python3 + bash deps) and `packages.default` for reproducible installs on NixOS / nix-darwin

## 🧪 Tests

- [ ] Update static tests AC17–AC20 after architecture refactor (regex patterns reference old global names)
- [ ] Add Playwright E2E tests for tab switching, chart toggle, auto-refresh cycle (optional, needs `playwright` dep in venv)

## 📦 Release Plan

| Version | What |
|---------|------|
| ~~v2026.2.18~~ | ✅ Security hardening |
| ~~v2026.2.19~~ | ✅ Performance + test suite |
| **v2026.2.20** | Architecture refactor (State/DataLayer/DirtyChecker/Renderer/Theme) |
| **v2026.2.21** | Volatile timestamp stableSnapshot fix + perf |
| **v2026.2.22** | Dockerfile + Nix flake |

## 🔖 Notes

- 44/44 tests passing (`test_frontend.py` + `test_data_schema.py` + `test_server.py` + `test_critical.py`)
- Architecture doc: `ARCHITECTURE.md`
- Test runner: `.venv/bin/pytest tests/ -v`
- Version format: `YYYY.M.D` (matching OpenClaw convention)
