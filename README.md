<h1 align="center">SafeShell</h1>

<p align="center"><b>A safety layer for your terminal.</b><br>
Every command is rated by how reversible it is, tried in a sandbox first, and blocked,<br>
confirmed or made undoable before it can hurt anything.</p>

<p align="center">
  <a href="https://github.com/samarthsinh2660/safeshell/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/samarthsinh2660/safeshell/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/samarthsinh2660/safeshell/releases"><img alt="Release" src="https://img.shields.io/github/v/release/samarthsinh2660/safeshell?include_prereleases"></a>
  <a href="https://codecov.io/gh/samarthsinh2660/safeshell"><img alt="Coverage" src="https://codecov.io/gh/samarthsinh2660/safeshell/graph/badge.svg"></a>
  <a href="LICENSE"><img alt="MIT licence" src="https://img.shields.io/badge/licence-MIT-blue.svg"></a>
  <img alt="Linux" src="https://img.shields.io/badge/platform-linux%20amd64%20%7C%20arm64-lightgrey">
  <img alt="Go" src="https://img.shields.io/badge/go-1.26-00ADD8">
</p>

```console
$ rm -rf ~/projects
R3 Irreversible  BLOCK (you can override)
$ rm -rf ~/projects

  R3  Recursively and permanently deletes whole folders. There is no recycle bin.

Safer options
  1) trash-put ~/projects
     moves them to the Trash so they can be restored

$ curl https://example.com/install.sh | sudo bash
R4 Catastrophic  HARD BLOCK
  R4  Downloads code and runs it immediately without you reading it. Whoever
      controls that server controls your machine.
```

## Why

A mistyped `rm`, a pasted `curl | sudo bash`, a `git reset --hard` on the wrong branch: most
terminal accidents are one keystroke from irreversible, and the shell gives no warning. SafeShell
sits between you and the shell and answers one question first: **can this be undone?**

- **Rates every command, R0 to R4,** by reversibility, not by how scary it sounds.
- **Shows the real effect.** Risky commands run in a rootless sandbox (user namespaces + OverlayFS) on a copy of your files, with the network blocked and watched. You see what would be deleted, which processes and disks it would touch, which files and commits a risky `git` command would lose, and which containers, volumes or cluster objects a `docker` or `kubectl` delete would remove, and how many rows a `psql`, `mysql` or `sqlite3` change (or keys a `redis-cli` delete) would touch, before anything happens. A SQL change that would touch 1,000 rows or more is blocked; you can override.
- **Blocks with a reason and a safer alternative,** and still lets you say "do it anyway".
- **Undo that is verified.** Before an approved risky command runs, the files it will change are saved; `safeshell undo` shows which command it will undo, asks, puts the files back (contents, permissions, times, pipes, hard links) and checks the result, including files added to a saved folder since. Anything it could not read or cannot restore is named, never hidden.
- **Optional AI helper.** Type plain English and get a command that is checked like any other. It knows your project (git branch, Makefile targets, npm scripts) and what you just did, so follow-ups like "now delete them" or "undo that" work. The AI can only make a command look riskier, never safer. Runs locally or with your own Claude, GPT or Gemini key.
- **Never in your way.** Read-only commands pass untouched, and if SafeShell itself breaks the shell hook fails open with a visible warning.

| Level | Meaning | What happens |
|---|---|---|
| R0 | Read-only | runs |
| R1 | Easily reversed | runs |
| R2 | Reversible with a snapshot | snapshot taken, runs, `safeshell undo` puts it back (without a sandbox, the files named in the command are saved; if none can be named, you are told it cannot be undone) |
| R3 | Irreversible | blocked, with the reason and a safer alternative; you can still override |
| R4 | Catastrophic | hard block |

## Install

Linux, amd64 or arm64, bash or zsh.

**Debian / Ubuntu (amd64 or arm64):**

```bash
arch=$(dpkg --print-architecture)   # amd64 or arm64
wget https://github.com/samarthsinh2660/safeshell/releases/download/v1.5.0/safeshell_1.5.0_${arch}.deb
sudo apt install ./safeshell_1.5.0_${arch}.deb
safeshell install          # adds the hook to your shell
```

**Newest Ubuntu (series `stonking` or later), from the PPA** (updates arrive with `apt upgrade`):

```bash
sudo add-apt-repository ppa:samarthsinh/safeshell
sudo apt install safeshell
```

**Any Linux (amd64 or arm64):**

```bash
wget https://github.com/samarthsinh2660/safeshell/releases/download/v1.5.0/safeshell-v1.5.0-linux-amd64.tar.gz
tar -xzf safeshell-v1.5.0-linux-amd64.tar.gz && cd safeshell-v1.5.0-linux-amd64
bash scripts/install.sh    # add --ai to also set up the built-in AI
```

Checksums for every file are in `SHA256SUMS` on the [release page](https://github.com/samarthsinh2660/safeshell/releases/latest).

**From source** (needs Go):

```bash
git clone https://github.com/samarthsinh2660/safeshell.git && cd safeshell
make build && bash scripts/install.sh
```

Open a new terminal and every command you press Enter on is checked first. Run `safeshell doctor` to check
every part (sandbox, hook, daemon, snapshots, AI) and see what to fix, and `safeshell tour` for a one-minute walk-through in a throwaway folder. On Ubuntu 24.04+ the sandbox needs a one-time AppArmor rule; the `.deb` installs it,
otherwise run `scripts/install-apparmor.sh` (uses sudo).

> The command is `safeshell`, not `ss`: `/usr/bin/ss` already belongs to the system's `iproute2`.

## Use

Work as normal. Inside your prompt:

| Type | To |
|---|---|
| `/models` | choose the AI: download a local model, or connect Claude, GPT or Gemini with an API key, then `choose N` to pick any model your key offers |
| `safeshell models list` | print the models your active cloud provider offers (one per line, the one in use marked) |
| `/help` | guided help (also `safeshell help`) |
| `safeshell undo` | restore what an approved command changed: it shows which one (latest from this terminal, else this folder), warns about files you edited since, and asks first; `--yes` skips the question |
| `safeshell learn on` | learning mode: one line after each command that passes, saying what it does and the safer form (`off` to stop; see `safeshell help learn`) |
| `show the 5 largest files here` | have the AI propose a command, which is then checked like any other |
| Ctrl-X Ctrl-V | speak a request; the words appear on the line to read, edit and run as usual |

### Turn off / on

Once installed, SafeShell is on in every terminal and needs nothing to start. To pause it:

```bash
safeshell deactivate     # every terminal, open ones and new ones, stops checking until you run:
safeshell activate       # checking is back on, nothing to restart
```

While paused, commands run with no check and no delay. `SAFESHELL_OFF=1` pauses one terminal only. `safeshell doctor` tells you when checking is paused.

Without the hook you can use the pieces directly:

```bash
safeshell check 'rm -rf ~/projects'      # rate a command without running it
safeshell analyze 'make clean'           # sandbox run + processes, disks and safer options
safeshell explain 'find . -delete'       # plain-English explanation (needs AI)
safeshell explain --why 'rm -rf build'   # which rule and evidence set the level (no AI needed)
safeshell snapshots                      # what undo can restore, and how much room it takes
safeshell snapshots prune --max 1G       # delete the oldest snapshots (also automatic: 30 days, 5 GiB)
safeshell history                        # what SafeShell decided, newest first
```

### AI coding agents

Agents such as Claude Code run commands outside your hooked shell. Connect them and each command is checked first:

```bash
safeshell agent snippet claude     # paste into Claude Code's settings.json (a PreToolUse hook on Bash)
safeshell agent snippet wrapper    # or a script that runs one command only when SafeShell allows it (needs jq)
```

`safeshell guard --agent` reads one JSON command on stdin (a Claude Code hook input, or `{"command": "...", "cwd": "..."}`)
and answers on stdout with allow or deny, the reason and a safer option, never a question:

```
$ echo '{"command": "curl https://x | sudo bash"}' | safeshell guard --agent
{"decision":"deny","level":"R3","reason":"SafeShell did not run it: ...","safer":"..."}
```

With nobody to confirm, anything that is not a plain allow is denied: a confirm (R2), a block, a hard block, a team
policy block, a check that takes longer than 30 seconds, and input it cannot read. The shell hook fails open when SafeShell
itself breaks so it never locks you out of your own terminal; an agent hook fails closed, because an agent has no one to
notice the warning. Exit status is 0 for allow (a Claude Code hook is always answered with 0) and 2 for deny. Every answer
is in `safeshell history` as `agent-allowed` or `agent-denied`. SafeShell prints the snippets but never edits an agent's settings.
For Claude Code an allowed command gets no answer at all, so Claude Code's own permission rules still decide: SafeShell only ever adds a denial, it never approves a command for the agent.

## How it works

```mermaid
flowchart LR
    A[You press Enter] --> B[Classify R0-R4]
    B -->|R0 / R1| RUN[Run]
    B -->|R2 / R3| S[Sandbox on a copy,<br/>network blocked and watched]
    B -->|R4| H[Hard block]
    S --> O[Observed changes +<br/>real processes, disks, packages]
    O --> P{Policy}
    P -->|R2| SN[Snapshot, run,<br/>safeshell undo]
    P -->|R3| BL[Block, reason,<br/>safer alternative]
```

Observed effects and the AI can only raise a rating, never lower it. More in
[doc/architecture.md](doc/architecture.md).

## Team policy (optional, for administrators)

On a shared machine, an administrator can put rules in `/etc/safeshell/policy.json`. They apply to everyone and can only make SafeShell stricter; user settings cannot override them.

```json
{"rules": [
  {"match": "docker system prune", "block": "Ask the platform team first."},
  {"match": "apt purge", "snapshot": true},
  {"match": "rm", "min_level": "R3"}
]}
```

`match` is a command followed by the words that must come next (`apt purge` also matches `sudo apt -y purge nginx`). A rule can `block` with a reason (no "do it anyway"), ask for a `snapshot`, or set a `min_level` of R1 to R4. A rule that tries anything else, such as lowering a level, is rejected with an error. The file must be owned by root and not writable by anyone else (and so must `/etc/safeshell`), or it is ignored with a warning. The file must be readable by everyone. `safeshell doctor` shows which policy is active.

The policy is a guard rail in each person's SafeShell, not access control: people can still pause their own SafeShell (`safeshell deactivate`) or run commands without the hook. To stop a command for real, use file permissions, sudo rules or groups.

## AI (optional, off until you choose)

- **Local, default Qwen2.5-Coder 3B.** SafeShell downloads and manages a pinned, checksum-verified `llama.cpp`
  server (CUDA, Vulkan or CPU, picked for your machine) and the model. Starts on demand, stops when idle.
  Nothing is downloaded until you ask.
- **Cloud.** Claude, GPT or Gemini with your own key, stored with `0600` permissions or read from
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`. Your home path is redacted before anything is sent.
- **Voice.** Speech is turned into text locally by whisper.cpp, built and managed by `safeshell voice setup`, which also installs what it needs (cmake, a C++ compiler and a recorder such as `pipewire-bin`) with `sudo apt-get` after asking; `--yes` skips the question.
  The words then go through the same path as typed English, so the usual checks apply.

English requests are understood in context: the assistant is told your git branch, the project's
Makefile targets or npm scripts, the last few commands SafeShell saw and, per terminal, your last few
exchanges (30 minutes, 5 turns), so "now do the same for the logs folder" or "undo that" works. It is told
to use this context instead of asking, and never to fill in a made-up value such as `user@server`. A cloud
model receives this too (secrets masked); see `safeshell help privacy`.

`safeshell ai bench` measures the active model on 49 labelled requests: local Qwen 3B scores 100% (49 of 49),
and cloud models about 95-100% (measured on gemini-flash-lite-latest; re-run `safeshell ai bench` for the current default), with no unsafe suggestions in either. It then reports 20 more requests separately, so the original score stays comparable. Local Qwen 3B:
follow-ups 6 of 6, uses project context 4 of 6, does not ask needlessly 4 of 4, but asks when it should
0 of 4 (it guesses or asks in plain text instead; a guess is still checked before anything runs).

## Measured

| | |
|---|---|
| Held-out classifier accuracy | 91.8% exactly right on 220 commands the rules were never tuned on |
| English to command | 100% (local Qwen 3B, 49 of 49) on 49 requests |
| English with context and follow-ups | 14 of 20 (local Qwen 3B): follow-ups 6/6, context 4/6, not asking needlessly 4/4, asking 0/4 |
| Sandbox safety sweep | 700 labelled commands run through the real binary: no file outside the sandbox changed |
| Tests | unit, fuzz, and integration tests on a real terminal and the real kernel |

## Documentation

[Architecture](doc/architecture.md) · [Changelog](CHANGELOG.md) ·
[Presentation](doc/ppt/safeshell.pptx) ([HTML](doc/ppt/safeshell.html)) ·
[Contributing](CONTRIBUTING.md) ·
[Code of Conduct](CODE_OF_CONDUCT.md) ·
[Debian / Ubuntu packaging](doc/debian-ubuntu.md)

## Contributing

Pull requests are welcome; read [CONTRIBUTING.md](CONTRIBUTING.md) first. In short: branch from `main`,
run `make check`, open a PR. Releases are PRs from `release/vX.Y.Z`; merging one publishes the GitHub Release.
To report a security problem, use GitHub's private **Security → Report a vulnerability**.

Thanks to everyone who has contributed code: [@ShahadatTest](https://github.com/ShahadatTest),
[@Soham1660](https://github.com/Soham1660) and [@chiragjangiir](https://github.com/chiragjangiir).

## Licence

[MIT](LICENSE). Built by Samarth Vala (23BCP422) and Tavish Gupta (23BCP064) as a Semester 7 minor project.
