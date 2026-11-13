# Changelog

All notable changes to SafeShell. Versions follow [semantic versioning](https://semver.org/).

## 1.1.1 - 2026-11-13

A safety release: it closes the worst classifier and hook bypasses and hardens the sandbox, the daemon socket and printing.

### Added
- `/models` → `choose N` and `safeshell models list` show the models your own API key can use and save your pick. (#131, #133)
- `safeshell explain --why <command>` shows which rule and which evidence set the level, without the AI. (#87)
- `safeshell voice setup` offers to install what it needs (`cmake`, `g++`, `make`, and `pipewire-bin` when there is no recorder) with `sudo apt-get`, after asking. `--yes` skips the question. (#136)

### Changed
- Cloud defaults: OpenAI `gpt-6-luna`, Gemini `gemini-3.5-flash-lite`; Claude is unchanged. The prompt ignores filler words, reads typos as the intended word, and asks instead of guessing a deletion for a vague "clean up". `safeshell ai bench` has 49 requests. (#131)
- An AI-proposed line that changes your shell (alias, trap, export, function, source, ...) asks `[y/N]` even when it is otherwise allowed. Lines you type yourself are unchanged. (#132)
- Commands in the sandbox have no terminal: `/dev/tty` is gone and they start in their own session. Ctrl-C no longer reaches them; the timeout still ends them. (#130)
- Without an absolute `XDG_RUNTIME_DIR` there is no daemon; commands are checked directly and `safeshell daemon` says why. (#83)
- CI runs one check per part and requires 100% coverage of changed lines. (#68)
- Internal clean-up: shared helpers, consistent messages, more tests. `internal/ai/cloud` is now `internal/ai/anthropic`. (#136)

### Fixed
- Every subcommand answers `-h`/`--help` with its help and does nothing else; `safeshell uninstall --help` used to uninstall. `--` ends the options, and an unknown option exits 2 and says what to run. (#63)
- Global options before a subcommand no longer hide it (`git -C repo reset --hard`), and `cp`/`tee` are rated by their target (`cp /dev/zero /dev/sda`). (#39, #40)
- `exec > /etc/passwd`, `bash -lc '...'`, `find -exec sh -c '...'` and wrapper options such as `exec -a ls rm -rf /` are looked into. (#36, #37, #38, #41)
- "Changes no files" is no longer trusted when the sandbox could not see a folder you can write to. (#50)
- The bash hook checks lines in vi mode and from Ctrl-X Ctrl-E, and keeps working under `set -u` / `setopt nounset`. (#58, #59)
- Security: the hook only talks to a daemon socket you own (checked with the peer's user id). File names, command output and AI text are printed with control and invisible characters shown as escapes, and an AI command with hidden characters is refused. Sandboxed commands can't reach your real terminal. (#83, #130, #132)

## 1.1.0 - 2026-11-12

### Added
- Voice: `safeshell voice` (or Ctrl-X Ctrl-V in the shell hook) turns speech into a request on your machine with whisper.cpp. The text then goes through the same checks as typed text. `safeshell voice setup` downloads and builds the engine and model, both pinned by checksum. (#13)
- The AI asks one short question when a request is missing a detail, instead of giving up. It never asks you to confirm a dangerous request. (#22)
- A `.deb` for arm64 as well as amd64. (#14)
- `--verbose` on `analyze` and `simulate` shows every sandbox note.

### Changed
- `analyze` and `simulate` show only the sandbox notes about folders the command touches. When that can't be worked out, every note is shown. (#12)
- JSON output: `sim.Warnings` is now a list of `{paths, text}` objects instead of strings.
- `safeshell ai bench` has 44 requests (one new ambiguous request where a question is the right answer).

### Fixed
- `\rm`, `/bin/rm` and `busybox rm` are recognised like `rm`. (#8)
- Running downloaded code is blocked in more forms: `source <(curl ...)`, `. <(curl ...)`, `eval "$(curl ...)"`, `sh -c "$(curl ...)"`, `sh -s` and `source /dev/stdin`. (#9, #17)
- Rule gaps: perl, ruby and python mass deletes, `cp /dev/null file`, `gpg --delete-secret-keys` and `git filter-branch`/`filter-repo` are rated by how hard they are to undo. (#11)
- `install.sh` asks before installing the AppArmor profile unless `--yes` is given. Thanks to @ShahadatTest for the first outside contribution. (#10)

## 1.0.0 - 2026-11-07

First stable release.

### Safety layer
- Rates every command R0 to R4 by how reversible it is, with a rule set covering files, disks, git, containers, Kubernetes, databases, packages, services, networking and cloud tools.
- Rootless sandbox (user, mount, PID and network namespaces with OverlayFS) that shows the real file changes, with the network blocked and every connection attempt recorded.
- Real-world checks for processes, disks, packages and services a command would affect.
- Blocks with a reason and a safer alternative; "do it anyway" is always the user's choice.
- Snapshots before approved risky commands, and `safeshell undo` with verification.
- bash and zsh hooks that fail open with a visible warning if SafeShell itself breaks; optional background daemon for faster checks.

### AI assistant (optional)
- Plain English to a command that is checked like any other; the AI can only raise a risk level.
- Built-in local model (default Qwen2.5-Coder 3B) with a managed, checksum-verified llama.cpp engine (CUDA, Vulkan or CPU), or your own Claude, GPT or Gemini key.
- `/models` menu, `safeshell help`, and `safeshell ai bench` (43 labelled requests).

### Packaging
- The command is `safeshell` (`/usr/bin/ss` belongs to `iproute2`).
- Linux amd64 and arm64 tarballs, a `.deb`, and a Debian source package; PPA at `ppa:samarthsinh/safeshell` for the newest Ubuntu.

### Measured
- 92.3% exact on a held-out set of 220 commands; 97% on the English-to-command benchmark (local Qwen 3B); 492 labelled commands run through the real binary with no change outside the sandbox.
