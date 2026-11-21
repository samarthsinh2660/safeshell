// Package shellhook holds the code a shell sources to route every command line
// through `safeshell guard` before it runs. It also binds Ctrl-X Ctrl-V to
// `safeshell voice --text`, which puts a spoken request on the command line
// for the person to read and edit before pressing Enter.
package shellhook

import (
	"fmt"
	"strings"
)

const bash = `# SafeShell hook for bash. Add to ~/.bashrc:  eval "$(safeshell hook bash)"
# Type plain English (or start a line with ?) and SafeShell AI turns it into a checked command.
# Press Ctrl-X Ctrl-V and speak: the words are put on the line to read and edit (set up with: safeshell voice setup).
__ss_bin=@BIN@   # the safeshell that printed this hook, so PATH does not matter
__ss_off="${XDG_STATE_HOME:-${HOME-}/.local/state}/safeshell/off"   # exists while checking is paused (safeshell deactivate)
__ss_pend=   # the lines already typed of a command that is not finished (here-document, open quote, trailing backslash)
__ss_noted=   # set once "checks turned off" is written to the audit log, so a terminal with SAFESHELL_OFF logs it once
__ss_note() { ("$__ss_bin" note "$@" >/dev/null 2>&1 &); }   # write an event to the audit log in the background; the prompt never waits
__ss_voice() {
  [[ -n "${SAFESHELL_OFF-}" ]] && return 0
  [[ -e "${__ss_off-}" ]] && return 0
  local out tty rc
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # Enter must end the recording
  out=$("$__ss_bin" voice --text </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  [[ $rc -eq 0 ]] || return 0
  READLINE_LINE=$out
  READLINE_POINT=${#READLINE_LINE}
}
__ss_guard() {
  [[ -z "${READLINE_LINE//[[:space:]]/}" && -z $__ss_pend ]] && return 0
  if [[ -n "${SAFESHELL_OFF-}" ]]; then [[ -n $__ss_noted ]] || { __ss_noted=1; __ss_note checks-off; }; return 0; fi
  __ss_noted=
  [[ -e "${__ss_off-}" ]] && return 0
  local out rc tty t first line=$__ss_pend$READLINE_LINE unknown=()
  if [[ -z $__ss_pend ]]; then   # only a first line can be plain English
    t=${READLINE_LINE#"${READLINE_LINE%%[![:space:]]*}"}; first=${t%%[[:space:]]*}
    # A first word the shell does not know may be plain English: let SafeShell AI try it.
    [[ -z "$(type -t -- "$first" 2>/dev/null)" ]] && unknown=(--unknown)
  fi
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # readline leaves the terminal raw, so Enter would not end an answer
  out=$("$__ss_bin" guard --multiline --cwd "$PWD" ${unknown[@]+"${unknown[@]}"} -- "$line" </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  case $rc in
    3) __ss_pend=$line$'\n'; return 0 ;;   # not finished: the whole command is checked when it is
    0) if [[ -z $__ss_pend ]]; then READLINE_LINE=$out
       elif [[ $out != "$line" ]]; then
         printf '\nA command typed over several lines cannot be replaced. Nothing was run. Type the replacement on one line.\n' >/dev/tty
         rc=1
       fi ;;
    1) READLINE_LINE= ;;   # cancelled
    *) # SafeShell itself failed (missing, crashed): say so and let the line through rather than lock the user out
       printf '\nSafeShell failed (exit %s), so this line was NOT checked. Set SAFESHELL_OFF=1 to silence this.\n' "$rc" >/dev/tty
       __ss_note not-checked "$rc" ;;
  esac
  [[ $rc -eq 1 && -n $__ss_pend ]] && { __ss_pend=; kill -INT $$; }   # drop the lines already typed
  __ss_pend=
  READLINE_POINT=${#READLINE_LINE}
}
__ss_reset() { __ss_pend=; }   # a new prompt means the last command is over, even if Ctrl-C ended it
__ss_noedit() { printf '\nOpening the line in an editor is turned off by SafeShell, because the edited line would run without a check. Edit it here and press Enter.\n' >/dev/tty; }
if [[ $- == *i* ]]; then
  shopt -s histverify   # !! and !$ are expanded into the line first, so the guard sees what would really run
  # Bind every keymap, so the guard also works in vi mode, whether it is switched on before or after this runs.
  for __ss_k in emacs vi-insert vi-command; do
    bind -m $__ss_k -x '"\C-x\C-s": __ss_guard'
    bind -m $__ss_k -x '"\C-x\C-v": __ss_voice'
    bind -m $__ss_k '"\e[9001~": accept-line'   # a sequence nobody types, so the guard cannot be skipped by accident
    bind -m $__ss_k '"\C-m": "\C-x\C-s\e[9001~"'
    bind -m $__ss_k '"\C-j": "\C-x\C-s\e[9001~"'   # Ctrl-J also accepts a line, and a pasted newline is one
    bind -m $__ss_k '"\e[9002~": operate-and-get-next'
    bind -m $__ss_k '"\C-o": "\C-x\C-s\e[9002~"'   # Ctrl-O runs the line and brings up the next one: guard it too
    bind -m $__ss_k -x '"\C-x\C-e": __ss_noedit'   # edit-and-execute-command runs the edited line unchecked
    bind -m $__ss_k -x '"\e#": __ss_noedit'   # insert-comment with a number removes the # and runs the line unchecked
  done
  bind -m vi-command -x '"v": __ss_noedit'
  bind -m vi-command -x '"#": __ss_noedit'
  unset __ss_k
  PROMPT_COMMAND="__ss_reset${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
  export SAFESHELL_HOOK=$$   # this shell's pid; lets safeshell doctor tell that the hook is active in this shell
fi
`

const zsh = `# SafeShell hook for zsh. Add to ~/.zshrc:  eval "$(safeshell hook zsh)"
# Type plain English (or start a line with ?) and SafeShell AI turns it into a checked command.
# Press Ctrl-X Ctrl-V and speak: the words are put on the line to read and edit (set up with: safeshell voice setup).
__ss_bin=@BIN@   # the safeshell that printed this hook, so PATH does not matter
__ss_off="${XDG_STATE_HOME:-${HOME-}/.local/state}/safeshell/off"   # exists while checking is paused (safeshell deactivate)
__ss_noted=   # set once "checks turned off" is written to the audit log, so a terminal with SAFESHELL_OFF logs it once
__ss_note() { ("$__ss_bin" note "$@" >/dev/null 2>&1 &) }   # write an event to the audit log in the background; the prompt never waits
__ss_voice() {
  emulate -L zsh
  [[ -n "${SAFESHELL_OFF-}" ]] && return 0
  [[ -e "${__ss_off-}" ]] && return 0
  local out tty rc
  zle -I
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # Enter must end the recording
  out=$("$__ss_bin" voice --text </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  (( rc == 0 )) || return 0
  BUFFER=$out
  CURSOR=${#BUFFER}
}
zle -N __ss_voice
bindkey '^X^V' __ss_voice
__ss_check() {   # returns 1 when the line was cancelled
  emulate -L zsh
  [[ -z "${BUFFER//[[:space:]]/}" && -z $PREBUFFER ]] && return 0
  if [[ -n "${SAFESHELL_OFF-}" ]]; then [[ -n $__ss_noted ]] || { __ss_noted=1; __ss_note checks-off; }; return 0; fi
  __ss_noted=
  [[ -e "${__ss_off-}" ]] && return 0
  local out rc tty first line=$PREBUFFER$BUFFER unknown=()   # PREBUFFER holds the lines already typed of a command that is not finished
  if [[ -z $PREBUFFER ]]; then   # only a first line can be plain English
    first=${${(z)BUFFER}[1]}
    whence -- "$first" >/dev/null 2>&1 || unknown=(--unknown)
  fi
  tty=$(stty -g </dev/tty) && stty sane </dev/tty
  out=$("$__ss_bin" guard --multiline --cwd "$PWD" "${unknown[@]}" -- "$line" </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  case $rc in
    3) ;;   # not finished: the whole command is checked when it is
    0) if [[ -z $PREBUFFER ]]; then BUFFER=$out
       elif [[ $out != "$line" ]]; then
         zle -I; print -u2 "\nA command typed over several lines cannot be replaced. Nothing was run. Type the replacement on one line."
         return 1
       fi ;;
    1) BUFFER=; return 1 ;;
    *) zle -I; print -u2 "\nSafeShell failed (exit $rc), so this line was NOT checked. Set SAFESHELL_OFF=1 to silence this."
       __ss_note not-checked "$rc" ;;
  esac
  return 0
}
setopt hist_verify   # expand !! into the line first so the guard sees what would really run
# Every widget that submits a line is wrapped, not just accept-line, so every keymap (viins, vicmd, emacs) is covered.
__ss_submit() {   # $1 is the built-in widget to finish with, whatever name the user's own widget was called by
  emulate -L zsh
  if ! __ss_check; then   # cancelled: a command typed over several lines is dropped whole, not run with an empty last line
    if [[ -n $PREBUFFER ]]; then zle send-break; else zle .accept-line; fi
    return
  fi
  zle .$1
}
for __ss_w in accept-line accept-and-hold accept-line-and-down-history accept-and-infer-next-history; do
  eval "__ss_${__ss_w//-/_}() { __ss_submit $__ss_w }"
  zle -N ${__ss_w} __ss_${__ss_w//-/_}
done
unset __ss_w
if [[ -o interactive ]]; then export SAFESHELL_HOOK=$$; fi   # this shell's pid; lets safeshell doctor tell that the hook is active in this shell
`

const fish = `# SafeShell hook for fish. Add to ~/.config/fish/conf.d/safeshell.fish:  safeshell hook fish | source
# Type plain English (or start a line with ?) and SafeShell AI turns it into a checked command.
# Press Ctrl-X Ctrl-V and speak: the words are put on the line to read and edit (set up with: safeshell voice setup).
# The safeshell that printed this hook is the one it calls, so PATH does not matter.
function __ss_bin
  @BIN@ $argv
end
if test -n "$XDG_STATE_HOME"
  set -g __ss_off $XDG_STATE_HOME/safeshell/off   # exists while checking is paused (safeshell deactivate)
else
  set -g __ss_off $HOME/.local/state/safeshell/off
end
function __ss_voice
  test -n "$SAFESHELL_OFF"; and return 0
  test -e "$__ss_off"; and return 0
  set -l tty (command stty -g </dev/tty 2>/dev/null)
  command stty sane </dev/tty 2>/dev/null   # Enter must end the recording
  set -l out (__ss_bin voice --text </dev/tty 2>/dev/tty)
  set -l rc $status
  test -n "$tty"; and command stty $tty </dev/tty 2>/dev/null
  test $rc -eq 0; or return 0
  set -l text (string join \n -- $out)
  commandline -r -- "$text"
  commandline -f end-of-buffer repaint
end
function __ss_note   # write an event to the audit log in the background; the prompt never waits
  __ss_bin note $argv >/dev/null 2>&1 &
  disown
end
function __ss_guard
  set -l line (commandline | string collect)
  if test -n "$SAFESHELL_OFF"
    # log "checks turned off" once per terminal, not for every line
    if not set -q __ss_noted; and not string match -qr '^\s*$' -- "$line"
      set -g __ss_noted 1
      __ss_note checks-off
    end
  else
    set -e __ss_noted
  end
  if test -n "$SAFESHELL_OFF"; or test -e "$__ss_off"; or string match -qr '^\s*$' -- "$line"
    commandline -f execute
    return 0
  end
  # fish would expand an abbreviation at the cursor only as it runs the line, after this check. Expand it now and wait
  # for Enter again, so what is checked is what runs. If the line did not change last time, the abbreviation does not apply here.
  set -l tok (commandline -t)
  if test -n "$tok"; and abbr -q -- "$tok"; and test "$line" != "$__ss_expanded"
    set -g __ss_expanded $line
    commandline -f expand-abbr repaint
    return 0
  end
  set -e __ss_expanded
  # fish keeps a whole multi-line command in one buffer, so the guard sees all of it at once. Only when fish itself
  # thinks the buffer is unfinished (or wrong) can the guard say "not finished" (exit 3): then fish adds a new line
  # or reports the error, and runs nothing.
  set -l multiline
  commandline --is-valid; or set multiline --multiline
  set -l unknown
  if not string match -q -- "*"\n"*" "$line"   # only a first line can be plain English
    # A first word fish does not know may be plain English: let SafeShell AI try it.
    type -q -- (commandline -o)[1]; or set unknown --unknown
  end
  set -l tty (command stty -g </dev/tty 2>/dev/null)
  command stty sane </dev/tty 2>/dev/null
  set -l out (__ss_bin guard $multiline --cwd "$PWD" $unknown -- "$line" </dev/tty 2>/dev/tty)
  set -l rc $status
  test -n "$tty"; and command stty $tty </dev/tty 2>/dev/null
  # Running a line from vi normal mode goes back to insert mode, as fish's own Enter does there.
  test "$fish_key_bindings" = fish_vi_key_bindings; and test "$fish_bind_mode" = default; and set fish_bind_mode insert
  switch $rc
    case 0
      set out (string join \n -- $out)
      test "$out" = "$line"; or commandline -r -- "$out"
      commandline -f execute
    case 1   # cancelled
      commandline -r ''
      commandline -f execute
    case 3   # not finished
      commandline -f execute
    case '*'   # SafeShell itself failed (missing, crashed): say so and let the line through rather than lock the user out
      printf '\nSafeShell failed (exit %s), so this line was NOT checked. Set SAFESHELL_OFF=1 to silence this.\n' $rc >/dev/tty
      __ss_note not-checked $rc
      commandline -f execute
  end
end
if status is-interactive
  # Bind every keymap, so the guard also works in vi mode. (fish cannot bind expand-abbr and a function to one key.)
  for __ss_m in default insert visual replace replace_one
    bind -M $__ss_m \r __ss_guard
    bind -M $__ss_m \n __ss_guard   # Ctrl-J also executes a line
    bind -M $__ss_m \cx\cv __ss_voice
  end
  set -e __ss_m
  set -gx SAFESHELL_HOOK $fish_pid   # this shell's pid; lets safeshell doctor tell that the hook is active in this shell
end
`

// FishQuote quotes s as one word for fish, where only \ and ' are special inside single quotes.
func FishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// Script returns the hook for the named shell. bin is the safeshell the hook
// calls, so the hook does not depend on PATH.
func Script(shell, bin string) (string, error) {
	q := "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
	switch shell {
	case "bash":
		return strings.Replace(bash, "@BIN@", q, 1), nil
	case "zsh":
		return strings.Replace(zsh, "@BIN@", q, 1), nil
	case "fish":
		return strings.Replace(fish, "@BIN@", FishQuote(bin), 1), nil
	}
	return "", fmt.Errorf("unsupported shell %q (use bash, zsh or fish)", shell)
}
