// Package shellhook holds the code a shell sources to route every command line
// through `safeshell guard` before it runs. It also binds Ctrl-X Ctrl-V to
// `safeshell voice --text`, which puts a spoken request on the command line
// for the person to read and edit before pressing Enter.
package shellhook

import "fmt"

const bash = `# SafeShell hook for bash. Add to ~/.bashrc:  eval "$(safeshell hook bash)"
# Type plain English (or start a line with ?) and SafeShell AI turns it into a checked command.
# Press Ctrl-X Ctrl-V and speak: the words are put on the line to read and edit (set up with: safeshell voice setup).
__ss_voice() {
  [[ -n "$SAFESHELL_OFF" ]] && return
  local out tty rc
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # Enter must end the recording
  out=$(command safeshell voice --text </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  [[ $rc -eq 0 ]] || return
  READLINE_LINE=$out
  READLINE_POINT=${#READLINE_LINE}
}
__ss_guard() {
  [[ -n "$SAFESHELL_OFF" || -z "${READLINE_LINE//[[:space:]]/}" ]] && return
  local out rc tty t first unknown=()
  t=${READLINE_LINE#"${READLINE_LINE%%[![:space:]]*}"}; first=${t%%[[:space:]]*}
  # A first word the shell does not know may be plain English: let SafeShell AI try it.
  [[ -z "$(type -t -- "$first" 2>/dev/null)" ]] && unknown=(--unknown)
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # readline leaves the terminal raw, so Enter would not end an answer
  out=$(command safeshell guard --cwd "$PWD" "${unknown[@]}" -- "$READLINE_LINE" </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  case $rc in
    0) READLINE_LINE=$out ;;
    1) READLINE_LINE= ;;   # cancelled
    *) # SafeShell itself failed (missing, crashed): say so and let the line through rather than lock the user out
       printf '\nSafeShell failed (exit %s), so this line was NOT checked. Set SAFESHELL_OFF=1 to silence this.\n' "$rc" >/dev/tty ;;
  esac
  READLINE_POINT=${#READLINE_LINE}
}
if [[ $- == *i* ]]; then
  shopt -s histverify   # !! and !$ are expanded into the line first, so the guard sees what would really run
  bind -x '"\C-x\C-s": __ss_guard'
  bind -x '"\C-x\C-v": __ss_voice'
  bind '"\C-x\C-a": accept-line'
  bind '"\C-m": "\C-x\C-s\C-x\C-a"'
  bind '"\C-j": "\C-x\C-s\C-x\C-a"'   # Ctrl-J also accepts a line, and a pasted newline is one
  bind '"\C-x\C-o": operate-and-get-next'
  bind '"\C-o": "\C-x\C-s\C-x\C-o"'   # Ctrl-O runs the line and brings up the next one: guard it too
fi
`

const zsh = `# SafeShell hook for zsh. Add to ~/.zshrc:  eval "$(safeshell hook zsh)"
# Type plain English (or start a line with ?) and SafeShell AI turns it into a checked command.
# Press Ctrl-X Ctrl-V and speak: the words are put on the line to read and edit (set up with: safeshell voice setup).
__ss_voice() {
  [[ -n "$SAFESHELL_OFF" ]] && return 0
  local out tty rc
  zle -I
  tty=$(stty -g </dev/tty) && stty sane </dev/tty # Enter must end the recording
  out=$(command safeshell voice --text </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  (( rc == 0 )) || return 0
  BUFFER=$out
  CURSOR=${#BUFFER}
}
zle -N __ss_voice
bindkey '^X^V' __ss_voice
__ss_check() {   # returns 1 when the line was cancelled
  [[ -n "$SAFESHELL_OFF" || -z "${BUFFER//[[:space:]]/}" ]] && return 0
  local out rc tty first unknown=()
  first=${${(z)BUFFER}[1]}
  whence -- "$first" >/dev/null 2>&1 || unknown=(--unknown)
  tty=$(stty -g </dev/tty) && stty sane </dev/tty
  out=$(command safeshell guard --cwd "$PWD" "${unknown[@]}" -- "$BUFFER" </dev/tty 2>/dev/tty)
  rc=$?
  [[ -n $tty ]] && stty "$tty" </dev/tty
  case $rc in
    0) BUFFER=$out ;;
    1) BUFFER=; return 1 ;;
    *) print -u2 "\nSafeShell failed (exit $rc), so this line was NOT checked. Set SAFESHELL_OFF=1 to silence this." ;;
  esac
  return 0
}
setopt hist_verify   # expand !! into the line first so the guard sees what would really run
# Every widget that submits a line is wrapped, not just accept-line.
for __ss_w in accept-line accept-and-hold accept-line-and-down-history accept-and-infer-next-history; do
  eval "__ss_widget_${__ss_w//-/_}() { __ss_check || { zle .accept-line; return }; zle .${__ss_w} }"
  zle -N ${__ss_w} __ss_widget_${__ss_w//-/_}
done
unset __ss_w
`

// Script returns the hook for the named shell.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bash, nil
	case "zsh":
		return zsh, nil
	}
	return "", fmt.Errorf("unsupported shell %q (use bash or zsh)", shell)
}
