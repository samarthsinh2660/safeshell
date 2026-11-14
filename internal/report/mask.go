package report

import "regexp"

var (
	secretAssign = regexp.MustCompile(`(?i)([\w-]*(?:token|key|secret|passw(?:or)?d)[\w-]*)=("[^"]*"|'[^']*'|\S+)`)
	secretHeader = regexp.MustCompile(`(?i)([\w-]*(?:authorization|token|key|secret|passw(?:or)?d)[\w-]*:\s*)[^\s'"]+(?:\s+[^\s'"]+)?`)
	secretJSON   = regexp.MustCompile(`(?i)("[\w-]*(?:token|key|secret|passw(?:or)?d)[\w-]*"\s*:\s*)"[^"]*"`)
	urlPassword  = regexp.MustCompile(`(://[^/\s:@]+):[^/\s@]+@`)
	userPassword = regexp.MustCompile(`((?:^|\s)(?:-u|--user)[\s=]+[^\s:]+):\S+`)
)

// MaskSecrets hides the values of secret-looking assignments (NAME=value where
// the name has TOKEN, KEY, SECRET, PASSWORD or PASSWD in it) and headers
// (Authorization: ..., X-Api-Key: ...), JSON fields with such names, passwords in
// web addresses (https://user:pass@host) and curl-style -u user:pass, so a command
// line can be stored safely.
// The names stay, so the line still shows what was done.
func MaskSecrets(line string) string {
	line = secretJSON.ReplaceAllString(line, `${1}"***"`)
	line = secretHeader.ReplaceAllString(line, "${1}***")
	line = urlPassword.ReplaceAllString(line, "${1}:***@")
	line = userPassword.ReplaceAllString(line, "${1}:***")
	return secretAssign.ReplaceAllString(line, "${1}=***")
}
