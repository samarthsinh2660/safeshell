package main

import (
	"fmt"
	"io"
	"strings"
)

type topic struct{ name, title, body string }

var helpTopics = []topic{
	{"start", "Get started in two minutes", `1. Add SafeShell to your shell (once):
     safeshell install            (adds the hook to ~/.bashrc or ~/.zshrc; undo with safeshell uninstall)
   Open a new terminal. From now on every command you press Enter on is checked first.
2. Get the built-in AI (optional, free, private, one download):
     safeshell ai setup
   or type  /models  to pick a different model, or to connect Claude, GPT or Gemini.
3. See it work, safely:
     safeshell tour               a one-minute walk-through in a throwaway folder
4. Try it:
     rm -rf ~/some-folder        SafeShell stops it, shows what would be lost, offers safer options
     show me the biggest files   English becomes a command you can read before it runs
     /help                       this guide, any time`},
	{"safety", "What SafeShell does to every command", `Each command is rated by how reversible it is:
   R0 only reads        R1 easy to undo        R2 recoverable with effort
   R3 irreversible      R4 can destroy the machine
It reads the command, then (for risky or unfamiliar ones) runs it inside a sandbox where nothing
real can change, and shows what it WOULD do: files deleted, processes killed, disks erased,
network contacted. The real result can only raise the rating, never lower it.
   R0-R1 run silently.  R2 asks you to confirm.  R3 is blocked with the reason and safer options;
   you may still choose "do it anyway".  R4 needs you to type a phrase.
Before an approved command runs, the files it will change are saved so  safeshell undo  can put them back.`},
	{"english", "Talk to your terminal in plain English", `Just type what you want:
     find the five largest files in this folder
     what is using port 8080
A line is treated as English when its first word is not a command and it reads like a sentence.
To force it, start the line with ?   (for example:  ? how much disk is free).
You always see the command first and press Enter to run it. It is then checked like any typed
command, so a dangerous suggestion is still stopped. Questions ("what does chmod 755 mean") are
answered and nothing runs. If something is unclear (several files match), SafeShell asks you a
short question first. Needs an AI model: see  safeshell help models.`},
	{"models", "Choose or change the AI model (/models)", `Type  /models  (in your terminal or as  safeshell models ) to see the menu:
   On this machine: Qwen models, downloaded once, private, free, work offline.
     Qwen2.5-Coder 3B is the default. 1.5B is faster, 7B is the most accurate (about 6 GB RAM).
   In the cloud: Claude (Anthropic), GPT (OpenAI), Gemini (Google). Paste your own API key; it is
     checked, then stored in ~/.config/safeshell/keys.json (readable only by you), or read from
     ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY. Only the command text is sent, with your
     home folder name hidden, and never file contents.
   To pick a model your key offers: in the menu type  choose N  (N is the cloud number), then a number
     from the list. Outside the menu,  safeshell models list  prints the same list. If the list cannot
     be fetched you are told why and the current model stays.
   Modes: auto (local if downloaded), local, cloud, hybrid (local first, cloud as backup), off.
Graphics: the engine uses CUDA on a recent NVIDIA driver, otherwise Vulkan, otherwise the CPU, and
   falls back automatically if the graphics card fails. See what is used with  safeshell doctor.
   Force one with "gpu": "cuda" | "vulkan" | "cpu" under ai.local in ~/.config/safeshell/config.json.
The AI can only make SafeShell MORE careful. It can never let a blocked command through.
Commands:  safeshell ai setup | safeshell ai status | safeshell ai stop`},
	{"voice", "Speak a request", `Set up once (installs the tools it needs with apt after asking, builds a small speech engine and
downloads a 148 MB English model; add --yes to skip the question):
     safeshell voice setup
Then, in your terminal, press Ctrl-X then Ctrl-V and speak. Press Enter when you are done.
Your words replace whatever is on the command line so you can read and edit them; press Enter as usual and
SafeShell turns them into a command and checks it like anything you typed.
     safeshell voice            speak, confirm the words, then run the request
     safeshell voice status     which recorder, engine and model it would use
Setup installs cmake, g++ and a recorder (pipewire-bin) if they are missing. The recording is deleted right
after it is read, and nothing leaves your machine: the speech engine runs locally.`},
	{"learn", "Learning mode: see what each command does", `Turn it on once:
     safeshell learn on         (off again with  safeshell learn off;  safeshell learn status  shows which)
Then every command that runs without a warning gets one line after it is checked, for example:
     $ cp a b
     Learn: R1 Reversible. Copies files. An existing file at the destination is overwritten.
The line says what the command does, its level and why, and adds "Safer: ..." when there is a safer form.
Commands that only read (R0) get no line. Commands SafeShell stops or asks about are already
explained in full, so they get nothing extra.
It only adds output: it never changes what is allowed or blocked, and it needs no AI.
The setting is "learn": true in ~/.config/safeshell/config.json.`},
	{"undo", "Undo and snapshots", `When you approve a risky command, SafeShell first saves the files it will change.
     safeshell undo            put back the most recent change and verify every file matches
     safeshell undo ID         undo a specific one
     safeshell snapshots       list what is saved
Undo restores deleted and changed files and removes files the command created. If anything cannot
be restored exactly, it tells you instead of claiming success.`},
	{"commands", "Every command", `   safeshell help [topic]            this guide
   safeshell check <cmd>             rate a command without running it
   safeshell analyze <cmd>           the full picture: rating, sandbox result, what it would hit
   safeshell simulate <cmd>          run in the sandbox and list the changes (nothing real is touched)
   safeshell explain <cmd>           the analysis plus a plain-English explanation (needs AI)
   safeshell explain --why <cmd>     which rule and evidence set the level (works without AI)
   safeshell ask <english>           turn English into a checked command
   safeshell learn [on|off|status]   learning mode: one line on what each passing command does
   safeshell undo / safeshell snapshots     restore / list
   safeshell models  (or /models)    choose a local model or connect Claude, GPT, Gemini
   safeshell ai setup|status|stop    manage the built-in AI
   safeshell voice [setup|status]    speak a request (Ctrl-X Ctrl-V in your terminal)
   safeshell install / safeshell uninstall  add or remove the hook in your shell startup file
   safeshell hook bash|zsh           print the shell hook
   safeshell version                 which release this is
   safeshell doctor                  check every part (sandbox, hook, daemon, snapshots, AI) and say what to fix
   safeshell tour                    a one-minute walk-through: a read, a blocked rm -rf, a snapshot, an undo
   Inside your terminal:  /models   /help`},
	{"privacy", "What leaves your machine", `Local mode: nothing. The model runs on your computer; the only download is the model itself.
Cloud mode: the command text you typed (or the English request, folder listing and the sandbox
summary used for the review) goes to the provider you chose, with your home folder name replaced
by ~. File contents are never sent. A log of decisions stays on your machine in
~/.local/state/safeshell/audit.jsonl.`},
	{"trouble", "Something is wrong", `safeshell doctor                     checks every part and says what to do about each problem
"sandbox unavailable"         Ubuntu 24.04+ needs a one-time rule: scripts/install-apparmor.sh
"model is not downloaded"     type /models and pick one, or run  safeshell ai setup
English is not recognised     start the line with ?   or run  safeshell ask "..."
Disable for one terminal      SAFESHELL_OFF=1        Turn AI off: /models then  off`},
}

// runHelp prints the overview, or one topic; an unknown topic goes to stderr.
func runHelp(args []string, w, stderr io.Writer) int {
	if len(args) > 0 {
		want := strings.ToLower(args[0])
		for _, t := range helpTopics {
			if t.name == want {
				fmt.Fprintf(w, "%s\n\n%s\n", t.title, t.body)
				return 0
			}
		}
		w = stderr // an unknown topic is an error
		fmt.Fprintf(w, "No help topic called %q.\n\n", args[0])
	}
	fmt.Fprint(w, "SafeShell stops dangerous commands before they run, shows what would happen, and lets you undo.\n\nTopics (safeshell help <topic>, or /help <topic> in your terminal):\n")
	for _, t := range helpTopics {
		fmt.Fprintf(w, "  %-9s %s\n", t.name, t.title)
	}
	fmt.Fprint(w, "\nNew here? Run:  safeshell help start\n")
	if len(args) > 0 {
		return 2
	}
	return 0
}
