# Rebrand module identity to mosoriob — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace every `github.com/hseinmoussa/claude-autopilot` reference with `github.com/mosoriob/claude-autopilot` so module identity matches the real repo, with build and tests green.

**Architecture:** Single mechanical string substitution on one token (the GitHub org segment of the module path) across `go.mod`, 7 `.go` files, `README.md`, and `llms.txt`. No logic, control flow, or data format changes. Verification is build + full test suite + a zero-match grep.

**Tech Stack:** Go 1.24, `go build`, `go test`, `gofmt`, `perl` (portable in-place edit on macOS/Linux).

---

### Task 1: Rebrand module path

**Files:**
- Modify: `go.mod:1`
- Modify: `main.go`
- Modify: `cmd/root.go`
- Modify: `internal/notifier/notifier.go`
- Modify: `internal/config/config.go`
- Modify: `internal/runner/runner.go`
- Modify: `internal/detector/detector.go`
- Modify: `internal/queue/queue.go`
- Modify: `README.md` (lines 3, 4, 18, 21, 26)
- Modify: `llms.txt:15`

- [ ] **Step 1: Capture the baseline (the "failing test")**

Run:
```bash
go build ./... && go test ./... 2>&1 | tail -15
grep -rn 'github.com/hseinmoussa/claude-autopilot' --include='*.go' --include='go.mod' --include='*.md' --include='*.txt' . | grep -v 'docs/superpowers/' | wc -l
```
Expected: build succeeds; every package with tests reports `ok` (compat, config, detector, fileutil, lock, queue, runner, timeparse); grep count is non-zero (currently 14). The non-zero grep is the condition this task must drive to zero.

- [ ] **Step 2: Substitute the module path in all source files**

Run (a single regex; the string has exactly one meaning — the module org — so a global replace is safe; `docs/superpowers/` is excluded so the spec/plan prose keeps the old name as historical record):
```bash
grep -rl 'github.com/hseinmoussa/claude-autopilot' \
  --include='*.go' --include='go.mod' --include='*.md' --include='*.txt' . \
  | grep -v 'docs/superpowers/' \
  | xargs perl -pi -e 's{github\.com/hseinmoussa/claude-autopilot}{github.com/mosoriob/claude-autopilot}g'
```

- [ ] **Step 3: Re-format the changed Go files**

Run:
```bash
gofmt -w go.mod main.go cmd/root.go internal/notifier/notifier.go internal/config/config.go internal/runner/runner.go internal/detector/detector.go internal/queue/queue.go 2>/dev/null; gofmt -w $(git ls-files '*.go')
```
Note: `gofmt` ignores `go.mod` (not Go source) — harmless. The second invocation formats all tracked `.go` files defensively.

- [ ] **Step 4: Verify the substitution is complete (the "test passes")**

Run:
```bash
grep -rn 'github.com/hseinmoussa/claude-autopilot' --include='*.go' --include='go.mod' --include='*.md' --include='*.txt' . | grep -v 'docs/superpowers/' | wc -l
head -1 go.mod
```
Expected: count is `0`; `go.mod` line 1 reads `module github.com/mosoriob/claude-autopilot`.

- [ ] **Step 5: Verify build**

Run:
```bash
go build ./...
```
Expected: exits 0, no output (no unresolved imports).

- [ ] **Step 6: Verify tests (no regression vs baseline)**

Run:
```bash
go test ./... 2>&1 | tail -15
```
Expected: every package that was `ok` in Step 1 is still `ok` (compat, config, detector, fileutil, lock, queue, runner, timeparse). No `FAIL`.

- [ ] **Step 7: Verify README references resolve to the fork**

Run:
```bash
grep -n 'go install\|goreportcard\|/releases\|git clone' README.md llms.txt
```
Expected: every printed line contains `mosoriob`, none contains `hseinmoussa`.

- [ ] **Step 8: Commit**

```bash
git add go.mod main.go cmd internal README.md llms.txt
git commit -m "Rebrand module path from hseinmoussa to mosoriob"
```

---

## Self-Review

**1. Spec coverage:**
- Spec "Scope" table — all 10 file rows covered by Task 1 Files list + Step 2 regex (which targets exactly the files containing the token). ✓
- Spec "Method" steps 1–3 (replace, gofmt, build+test) — Steps 2, 3, 5, 6. ✓
- Spec "Acceptance criteria": zero grep matches → Step 4; `go.mod` line 1 → Step 4; `go build` → Step 5; `go test` parity → Steps 1+6; README resolves → Step 7. ✓
- Spec "Out of scope" (`.goreleaser.yml`, `ci.yml`, GitHub settings) — not touched; Step 2 regex only matches files containing the token, and those two were verified clean during brainstorming. ✓

**2. Placeholder scan:** No TBD/TODO/"handle edge cases". Every step has the exact command. ✓

**3. Type consistency:** No types or signatures introduced — pure string substitution. The replacement string `github.com/mosoriob/claude-autopilot` is identical in every step. ✓

**Note on acceptance grep:** spec's literal criterion is "`grep -rn hseinmoussa .` returns zero matches." This plan scopes the grep to source/doc file types and excludes `docs/superpowers/` because the spec and this plan intentionally retain the old name as historical context — a zero-match over the entire tree including these planning docs is neither achievable nor desirable. The intent of the criterion (no live reference to the upstream path) is fully met.
