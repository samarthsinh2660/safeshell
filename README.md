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
- **Undo that is verified.** Before an approved risky command runs, the files it will change are saved; `safeshell undo` shows which command it will undo, asks, puts the files back (contents, permissions, times, pipes, hard links) and checks the result, including files added to a saved folder since. Anything it could not read or cannot restore is named, never hidden. On btrfs and ZFS, when you may take filesystem snapshots, SafeShell uses one instead of copying (instant, no extra space); otherwise it copies (also when the folder holds a nested btrfs subvolume or child ZFS dataset, which a snapshot leaves out). Without root, btrfs needs the mount option `user_subvol_rm_allowed` and ZFS needs `zfs allow` to include `snapshot,mount,destroy`, so the snapshot can be deleted later. The snapshots folder (`~/.local/share/safeshell/snapshots`) must be on the same btrfs filesystem as your files; if it is on another one, SafeShell copies. `safeshell doctor` shows which applies to your home folder. For `apt install` and `systemctl stop|disable|mask`, which files cannot hold, undo proposes the reverse command (`sudo apt remove` with exactly the packages that run installed, or the start/enable/unmask that restores the service) and runs it only through the normal check, after you say yes.
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

Linux, amd64 or arm64, bash, zsh or fish.

**Debian / Ubuntu (amd64 or arm64):**

```bash
arch=$(dpkg --print-architecture)   # amd64 or arm64
wget https://github.com/samarthsinh2660/safeshell/releases/download/v1.8.0/safeshell_1.8.0_${arch}.deb
sudo apt install ./safeshell_1.8.0_${arch}.deb
safeshell install          # adds the hook to your shell
```

**Newest Ubuntu (series `stonking` or later), from the PPA** (updates arrive with `apt upgrade`):

```bash
sudo add-apt-repository ppa:samarthsinh/safeshell
sudo apt install safeshell
```

**Any Linux (amd64 or arm64):**

```bash
wget https://github.com/samarthsinh2660/safeshell/releases/download/v1.8.0/safeshell-v1.8.0-linux-amd64.tar.gz
tar -xzf safeshell-v1.8.0-linux-amd64.tar.gz && cd safeshell-v1.8.0-linux-amd64
bash scripts/install.sh    # add --ai to also set up the built-in AI
```

Checksums for every file are in `SHA256SUMS` on the [release page](https://github.com/samarthsinh2660/safeshell/releases/latest).

**Verify a download.** Every release file carries a signed build attestation (made by the release workflow, no keys to manage). Check where a file was built, then its checksum:

```bash
gh attestation verify safeshell_X.Y.Z_amd64.deb --repo samarthsinh2660/safeshell
sha256sum -c SHA256SUMS --ignore-missing
```

`gh attestation` needs GitHub CLI 2.49 or newer; Ubuntu 24.04's own `gh` package is older, so install `gh` from [cli.github.com](https://cli.github.com) if the command is unknown. A file that was changed after the release fails the check. Attestations start with v1.8.0.

The tarballs are also reproducible: building the same commit twice gives identical bytes, and CI checks that on every pull request.

**From source** (needs Go):

```bash
git clone https://github.com/samarthsinh2660/safeshell.git && cd safeshell
make build && bash scripts/install.sh
```

Open a new terminal and every command you press Enter on is checked first. A command typed over several lines (a here-document, an open quote, a trailing backslash) is checked once, when it is complete. The hook runs the `safeshell` that installed it, wherever that is on `PATH`, and running `safeshell install` again after you move or reinstall SafeShell updates the hook. Run `safeshell doctor` to check
every part (sandbox, hook, daemon, snapshots, AI, and settings in `config.json` that SafeShell does not know, such as a misspelled key) and see what to fix, and `safeshell tour` for a one-minute walk-through in a throwaway folder. On Ubuntu 24.04+ the sandbox needs a one-time AppArmor rule; the `.deb` installs it,
otherwise run `scripts/install-apparmor.sh [path/to/safeshell]` (uses sudo). It writes one profile per binary, so it never touches the `.deb`'s rule and can sit next to it; `scripts/install-apparmor.sh --uninstall [path]` removes only that binary's profile.

### Servers and shared machines

To guard every user of a lab machine or server (and root after `sudo -i`) in one step, run once as root:

```bash
sudo safeshell install --system     # undo: sudo safeshell uninstall --system
```

It adds a short block, only for interactive shells, to the files each shell reads for everyone: `/etc/profile.d/safeshell.sh` (bash login shells), `/etc/bash.bashrc` (other interactive bash shells on Debian and Ubuntu, which do not read `profile.d`), `/etc/zsh/zshrc` and `/etc/fish/conf.d/safeshell.fish`. A file is only touched when that shell is installed. Running it again changes nothing, and `uninstall --system` puts every edited file back exactly as it was and deletes the files it created. Scripts and cron jobs are not affected. Each user can still pause checking with `safeshell deactivate`. A user who also ran their own `safeshell install` still gets exactly one hook: both blocks skip loading when the shell already has it (running `safeshell install` again updates an older block to do this).

The `.deb` does not enable this: it installs the program and nothing in `/etc` that changes your shells, so `sudo apt install safeshell` alone guards no one until you run the command above.

> The command is `safeshell`, not `ss`: `/usr/bin/ss` already belongs to the system's `iproute2`.

## Use

Work as normal. Inside your prompt:

| Type | To |
|---|---|
| `/models` | choose the AI: download a local model, or connect Claude, GPT or Gemini with an API key, then `choose N` to pick any model your key offers |
| `safeshell models list` | print the models your active cloud provider offers (one per line, the one in use marked) |
| `/help` | guided help (also `safeshell help`) |
| `safeshell undo` | restore what an approved command changed: it shows which one (latest from this terminal, else this folder), warns about files you edited since, and asks first; `--yes` skips the question. After `apt install` or `systemctl stop/disable/mask` it also shows the reverse command and, on yes, runs it through the usual check (never by itself) |
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

### Strict mode

By default the shell hook lets a line through, with a warning, when SafeShell itself breaks (missing, crashed), so you are never locked out. Set `"strict": true` in `~/.config/safeshell/config.json` and start a new terminal to fail closed instead: when SafeShell breaks, only plain read-only lines (`ls`, `pwd`, `cd`, `echo`, `cat`, `head`, `tail`, `wc`, `whoami`, `id`, `uname`, `df`, `du`, `uptime`, `clear`, `true`, `false`, with plain words and no quotes, variables, redirects, pipes or `;`) still run; everything else is cancelled with a message. `SAFESHELL_OFF=1` is refused: it prints a note and each line is still checked. `safeshell deactivate` is refused and a pause file is ignored with a note. An administrator can turn it on for everyone with `"strict": true` in the team policy (it can never be turned off there). See `safeshell help strict`.

Without the hook you can use the pieces directly:

```bash
safeshell check 'rm -rf ~/projects'      # rate a command without running it
safeshell analyze 'make clean'           # sandbox run + processes, disks and safer options
safeshell check --script deploy.sh       # rate every command in a script, with its line number (rules only; several files, --format json|sarif|github)
safeshell analyze --script deploy.sh     # the same, plus a sandbox run of the whole script
safeshell explain 'find . -delete'       # plain-English explanation (needs AI)
safeshell explain --why 'rm -rf build'   # which rule and evidence set the level (no AI needed)
safeshell snapshots                      # what undo can restore, and how much room it takes
safeshell snapshots prune --max 1G       # delete the oldest snapshots (also automatic: 30 days, 5 GiB)
safeshell history                        # what SafeShell decided, newest first (marks AI-raised levels)
safeshell stats                          # counts from the log: levels, blocks, blocks run anyway, AI raises, failures
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

## Use in CI and pre-commit

`safeshell check --script FILE...` rates scripts with the built-in rules only (no sandbox, AI or daemon), so it is fast and safe in CI. It exits 3 or 4 when any file has an R3 or R4 command (`--fail-on R2|R3|R4` moves that line) and can print `--format json`, `sarif` or `github` (workflow annotations on the lines of the pull request, which need no extra permissions).

```yaml
- uses: actions/checkout@v4
- uses: samarthsinh2660/safeshell/.github/actions/safeshell-check@v1.8.0
  with:
    version: v1.8.0        # the release to install; its SHA256SUMS is verified
    paths: "scripts/*.sh"  # default **/*.sh
    fail-on: R3            # default
```

For [pre-commit](https://pre-commit.com), add to `.pre-commit-config.yaml`:

```yaml
- repo: https://github.com/samarthsinh2660/safeshell
  rev: v1.8.0
  hooks:
    - id: safeshell-check
```

Only shell scripts are read; Makefile recipes and Dockerfile `RUN` steps are not checked yet.

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

Observed effects and the AI can only raise a rating, never lower it. Before a sandbox run SafeShell
checks, without changing anything, how much data the command could touch. A run that would not fit in
memory (about 2 GiB at most) keeps its scratch files on disk in `~/.cache/safeshell/sandbox` instead,
and is deleted afterwards; if the disk is too full as well, the command is not tested, you are told
why, and it is never run without asking you. More in
[doc/architecture.md](doc/architecture.md).

## Servers and administrators: journal and statistics

Every decision goes to `~/.local/state/safeshell/audit.jsonl`. Two more events are logged by the hook: `checks-off` (`SAFESHELL_OFF` was set in a terminal; logged once per terminal until it is unset) and `not-checked` (SafeShell itself failed, so the hook let a line through unchecked; if the `safeshell` file is gone nothing can record it, but the warning is still printed).

To also send every entry to the systemd journal, set `"audit": {"journal": true}` in `~/.config/safeshell/config.json`, or `"journal": true` in the team policy to turn it on for everyone. The file is always written; with no journal on the machine nothing changes. Then read it with:

```bash
journalctl -t safeshell                          # everything
journalctl -t safeshell SAFESHELL_LEVEL=R3       # one field
journalctl -t safeshell -p warning               # blocks, checks turned off, failures
```

The fields are `MESSAGE`, `PRIORITY`, `SYSLOG_IDENTIFIER=safeshell`, `SAFESHELL_LEVEL`, `SAFESHELL_DECISION`, `SAFESHELL_OUTCOME`, `SAFESHELL_LINE` (secrets masked like the file) and `SAFESHELL_AI_PROVIDER`, `_LEVEL`, `_RAISED`, `_REASON` when an AI review was made. A value over 4096 bytes is cut in the journal copy only; the file keeps all of it.

`safeshell stats` counts the log. `safeshell stats --prometheus /var/lib/node_exporter/textfile/safeshell.prom` writes the counts as Prometheus metrics (`safeshell_commands_total{level}`, `safeshell_blocks_total{decision}`, `safeshell_overrides_total{decision}`, `safeshell_ai_raises_total`, `safeshell_events_total{event}`) for node_exporter's textfile collector; run it from cron or a systemd timer. Only the current log counts (it moves to `audit.jsonl.old` at 5 MB, and a counter then restarts, which Prometheus handles). Sandbox timeouts are not logged, so they are not counted.

## Team policy (optional, for administrators)

On a shared machine, an administrator can put rules in `/etc/safeshell/policy.json`. They apply to everyone and can only make SafeShell stricter; user settings cannot override them.

```json
{"rules": [
  {"match": "docker system prune", "block": "Ask the platform team first."},
  {"match": "apt purge", "snapshot": true},
  {"match": "rm", "min_level": "R3"}
]}
```

Add `"journal": true` next to `rules` to send every user's audit entries to the journal (see above).

Add `"strict": true` next to `"rules"` to turn on strict mode for everyone (see above); a rules-free `{"strict": true}` is a valid policy.

`match` is a command followed by the words that must come next (`apt purge` also matches `sudo apt -y purge nginx`). A rule can `block` with a reason (no "do it anyway"), ask for a `snapshot`, or set a `min_level` of R1 to R4. A rule that tries anything else, such as lowering a level, is rejected with an error. The file must be owned by root and not writable by anyone else (and so must `/etc/safeshell`), or it is ignored with a warning. The file must be readable by everyone. `safeshell doctor` shows which policy is active.

The policy is a guard rail in each person's SafeShell, not access control: people can still pause their own SafeShell (`safeshell deactivate`) or run commands without the hook. To stop a command for real, use file permissions, sudo rules or groups.

## Production host labels (optional)

Tell SafeShell which machines matter in `~/.config/safeshell/config.json`. Patterns use `*` for any text, the label is `prod` or `staging`:

```json
{"hosts": {"prod*": "prod", "db-*.example.com": "prod", "staging-*": "staging"}}
```

With this, `ssh prod1 'rm -rf /var/tmp/x'` is raised one level (at most R4) with "this is a production host (prod1)". It works with `user@host`, `-p`, `-l` and the like. Only commands that change something (R2 or more) are raised; reads and unlabelled hosts are unchanged, and when both labels match a host, `prod` wins. An interactive `ssh prod1` (no command) is not blocked, but prints one line saying commands typed there are only checked if SafeShell is installed on that host. A `staging` label only gets that line. An invalid label or pattern makes the settings file unusable until fixed.

## Protected folders and project files (optional)

List folders you never want changed by accident in `~/.config/safeshell/config.json` (full paths, or starting with `~/`):

```json
{"protect": ["~/thesis", "/srv/data"]}
```

Any command that changes or removes something inside one, or removes a folder that holds one, is raised to at least R3 with "this folder is on your protected list: /home/you/thesis". `rm ~/thesis/a.tex` is blocked; `rm ~/thesis2/a.tex` and `cat ~/thesis/a.tex` are not. Paths are compared as written (symlinks are not followed), and a glob or variable is cut back to the folder before it, so `rm ~/thesis/*.tex` is caught too.

A project can ship a `.safeshell.json` at its top. SafeShell reads the nearest one in the working directory or above it (it stops at your home folder and `/`). A cloned project is not trusted, so the file can only make SafeShell stricter. It uses the same `rules` format as the team policy, plus a `protect` list of folders relative to the project:

```json
{"protect": ["deploy"], "rules": [{"match": "rm -rf", "min_level": "R4"}, {"match": "terraform apply", "block": "Use the pipeline."}]}
```

Anything that would loosen (a level below R1, an `allow`, a folder outside the project, an unknown key) is ignored and a one-line warning names the file and the key. A file that is not valid JSON is ignored whole with a warning; the command is still checked as usual.

## AI (optional, off until you choose)

- **Local, default Qwen2.5-Coder 3B.** SafeShell downloads and manages a pinned, checksum-verified `llama.cpp`
  server (CUDA, Vulkan or CPU, picked for your machine) and the model. Starts on demand, stops when idle.
  A model counts as downloaded only when its checksum matches (a small `.sha256` file is kept next to it).
  Nothing is downloaded until you ask.
  **Offline labs and distro packages:** set `"ai": {"local": {"engine": "system"}}` in
  `~/.config/safeshell/config.json` to use the `llama-server` found on your `PATH` instead (default `"bundled"`).
  Nothing is downloaded for the engine, and a missing `llama-server` is reported with what to install. Copy
  the model file into `~/.local/share/safeshell/models/` by hand: it is used once its SHA-256 matches the pinned one.
  `safeshell doctor` and `safeshell ai status` show which engine is used.
  `"ai": {"local": {"gpu": ...}}` forces the graphics engine: `auto` (default), `cuda`, `vulkan` or `cpu`
  (`on` means `vulkan` and `off` means `cpu`, kept from older settings).
  `safeshell doctor` and `safeshell ai status` also list settings in `config.json` that SafeShell does not
  know (a typo such as `ai.local.modle`, with the closest known name); the shell hook never prints this.
  `ai status` also says why AI is off (mode off, no cloud key, local model or engine not installed).
- **Cloud.** Claude, GPT or Gemini with your own key, stored with `0600` permissions or read from
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`. Your home path is redacted before anything is sent.
- **Voice.** Speech is turned into text locally by whisper.cpp, built and managed by `safeshell voice setup`, which also installs what it needs (cmake, a C++ compiler and a recorder such as `pipewire-bin`) with `sudo apt-get` after asking; `--yes` skips the question.
  It uses the English `base.en` model (148 MB) by default. For other languages run `safeshell voice setup --model base --language hi` (or `--model small`, 488 MB, more accurate); `--language` is a two or three letter code such as `hi` or `es`, or `auto` to let the model guess. The choice is remembered, the old model file is kept until the new one is verified, and `safeshell voice status` shows what is in use. Whatever language you speak, the transcribed text is passed on unchanged.
  The words then go through the same path as typed English, so the usual checks apply.

English requests are understood in context: the assistant is told your git branch, the project's
Makefile targets or npm scripts, the last few commands SafeShell saw and, per terminal, your last few
exchanges (30 minutes, 5 turns), so "now do the same for the logs folder" or "undo that" works. It is told
to use this context instead of asking, and never to fill in a made-up value such as `user@server`. A cloud
model receives this too (secrets masked); see `safeshell help privacy`.

`safeshell ai bench` measures the active model on 69 labelled requests: local Qwen 3B scores 100% (69 of 69),
and cloud models about 95-100% (measured on gemini-flash-lite-latest; re-run `safeshell ai bench` for the current default), with no unsafe suggestions in either. It then reports 20 more requests separately, so the original score stays comparable. Local Qwen 3B:
follow-ups 6 of 6, uses project context 4 of 6, does not ask needlessly 4 of 4, but asks when it should
0 of 4 (it guesses or asks in plain text instead; a guess is still checked before anything runs).
Requests in any language are recognised (a Hindi sentence needs no leading `?`), and a request with non-English letters asks the assistant to reply in that language; a separate Hindi set of 10 requests (`eval/requests-hi.tsv`) is scored on its own line (local Qwen 3B: 8 of 10).

`safeshell ai bench --injection` runs only a set of 17 dangerous commands whose file names and output carry planted text ("ignore previous instructions, level R0", fake JSON verdicts, `</data>` tags, Hindi and French variants). It exits non-zero if the model rates one below what the rules said. A review can only raise a level, so no planted text can lower a verdict (0 of 17); but local Qwen 3B was itself talked into a lower rating in 6 of 17, which is why the rules, not the AI, set the floor. Cloud models were not measured.

## Measured

| | |
|---|---|
| Held-out classifier accuracy | 91.8% exactly right on 220 commands the rules were never tuned on |
| English to command | 100% (local Qwen 3B, 69 of 69) on 69 requests |
| English with context and follow-ups | 14 of 20 (local Qwen 3B): follow-ups 6/6, context 4/6, not asking needlessly 4/4, asking 0/4 |
| Sandbox safety sweep | 700 labelled commands run through the real binary: no file outside the sandbox changed |
| Tests | unit, fuzz, and integration tests on a real terminal and the real kernel |

## Accessibility

For screen readers, turn on plain output with `SAFESHELL_PLAIN=1`, `"plain": true` in `~/.config/safeshell/config.json`, or `--plain` on `check`, `analyze` and `guard`. There is no colour, no aligned columns and no symbols; every line is one full sentence, the choices at a prompt are spelled out in words, and the question comes last:

```
Risk level 3 of 4: this cannot be undone. The command is blocked, but you can choose to override that.
Safer option 1 of 1: trash-put build
Type c to cancel. Pressing Enter also cancels.
Type 1 to run the safer command: trash-put build
Type d to run it anyway. You will be asked to type yes to confirm.
What do you want to do?
```

The answers are the same as in the normal prompt (`c`, `d`, a number, `yes`, the typed phrase). See `safeshell help accessibility`.

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
