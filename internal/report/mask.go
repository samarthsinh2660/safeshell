package report

import "regexp"

var (
	secretAssign = regexp.MustCompile(`(?i)([\w-]*(?:token|key|secret|passw(?:or)?d)[\w-]*)=("[^"]*"|'[^']*'|\S+)`)
	secretHeader = regexp.MustCompile(`(?i)([\w-]*(?:authorization|token|key|secret|passw(?:or)?d)[\w-]*:\s*)[^\s'"]+(?:\s+[^\s'"]+)?`)
)

// MaskSecrets hides the values of secret-looking assignments (NAME=value where
// the name has TOKEN, KEY, SECRET, PASSWORD or PASSWD in it) and headers
// (Authorization: ..., X-Api-Key: ...) so a command line can be stored safely.
// The names stay, so the line still shows what was done.
func MaskSecrets(line string) string {
	line = secretHeader.ReplaceAllString(line, "${1}***")
	return secretAssign.ReplaceAllString(line, "${1}=***")
}
