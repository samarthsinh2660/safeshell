# CLAUDE.md

Behavioral guidelines to reduce common LLM coding mistakes. Merge with project-specific instructions as needed.

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

## 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.

---

# SafeShell project specifics

Go module `github.com/samarthsinh2660/safeshell` is at the repository root. Read `README.md` and `doc/architecture.md` before structural work.

## Workflow (pull requests only)
- `main` is protected: nothing is committed to it directly. Every change is a branch + PR, CI green, then merged.
- Branch names: `feat/...`, `fix/...`, `chore/...`, `docs/...`, `refactor/...`, `test/...`. One concern per PR; fewer, bigger commits inside it.
- Commit messages: short, conventional (`feat(ai): ...`, `fix(sandbox): ...`), no co-author lines. PR descriptions follow `.github/pull_request_template.md`.
- Never push, open or merge a PR unless the user asks. Never force-push or rewrite history without approval.
- Before opening a PR run, `make check`; for sandbox/hook/daemon/snapshot changes also `make build && make integration`.

## Releases
- A release is a PR from a branch named exactly `release/vX.Y.Z` into `main`. Merging it triggers `.github/workflows/release.yml`, which tags, builds tarballs + `.deb` + `SHA256SUMS`, and creates the GitHub Release with the files attached.
- The release PR contains only release prep (docs, changelog notes, version mentions). Features land through their own PRs first.
- Version comes from the branch name via `-X main.version`; semver. `.deb` version is the tag without `v`.
- Follow the checklist in `doc/releasing.md` (release plan and milestones live there too).
- See `CONTRIBUTING.md` (diagrams of the PR and release flow), `CODE_OF_CONDUCT.md` and `doc/debian-ubuntu.md`; keep them in sync when the workflow changes.
- Users download the `.deb` (or tarball) from the GitHub Release. Getting into Debian/Ubuntu's own repositories is a separate, manual process described in the README.
- One-time repo setup (owner only): branch protection on `main` requiring the `check` job. No secrets are needed.

## Architecture rules
- Hexagonal. `internal/domain`, `classify`, `policy`, `report` are pure: no file, process, network or env access, no imports of adapters.
- `internal/engine` depends only on interfaces defined in the engine package (consumer-side ports). Adapters (`sandbox`, `snapshot`, `ai/*`, `runtime`, `ipc`) implement them and never import `engine`.
- `cmd/safeshell` is the only composition root: it wires adapters to the engine. No business logic there beyond argument handling and printing.
- Safety invariants: effects and the AI can only raise a risk level, never lower it. Evidence order ESTIMATED < INSPECTED < PREVIEWED < OBSERVED. Anything unknown, failed or timed out stays at or above its rule level and is not run unattended. The hook fails open with a visible warning if SafeShell itself breaks.
- The sandbox must never modify real files; anything that needs a real side effect is previewed or inspected read-only.
- New shells, snapshot backends, AI providers or inspectors are new adapters/files, not edits spread through the engine.

## Packaging rules
- The command is `safeshell`, never `ss` (that name belongs to `iproute2`).
- `debian/` must keep building offline on Debian unstable with `lintian` free of errors (command in `doc/debian-ubuntu.md`). Non-Go files the code embeds go in `DH_GOLANG_INSTALL_EXTRA` in `debian/rules`.
- Don't add a dependency that Debian does not package.

## Coding rules
- Follow the `golang` skill and standard Go style; `gofmt` clean; `go vet` clean; race-detector clean.
- Minimum code that solves the problem (see sections above). No speculative options, no abstractions for single use, no dead code.
- Errors: wrap with `%w`, say what the user can do, never quote response bodies or secrets. Context passed first; no global mutable state except the package-level test seams in `cmd/safeshell` (variables like `backend`, `guardInput`, `analyze`, `newVoice` that tests replace; each has a comment saying so).
- User-facing text is plain English (no jargon or internal terms), short, and says the next step.
- Linux only, amd64 + arm64 (riscv64 must vet). CGO off. New dependencies need a reason in the PR.

## Testing rules
- Every behaviour change has a test; every bug fix has a test that failed before. Table tests for classifier rules.
- Classifier: edit `eval/corpus` (dev) freely; `eval/heldout` is independent and only grows from new, unseen cases. `TestHeldoutReport` enforces the accuracy floors; never lower a floor to pass.
- Integration tests (`test/integration`) use the real binary and kernel and skip themselves when the sandbox is unavailable. `SS_SWEEP=1 go test ./test/integration -run Sweep` pushes every labelled command through the real binary.
- AI: `safeshell ai bench` (49 requests, fixed `eval.BenchEnv`) is the accuracy measure; cloud scores vary (about 95-100% Gemini flash-lite, 97% local Qwen 3B). Unit tests use fake backends, never the network.
- Live provider tests are opt-in and take keys from env vars only.

## Security and secrets
- Never put API keys, tokens or passwords in the repo, config files, tests, logs or commit messages. Keys live in `keys.json` (0600) or env vars.
- Do not run downloaded binaries or work around permission denials without the user's approval.
