package rules

import (
	"path"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// senders put data on the network.
var senders = map[string]bool{"nc": true, "ncat": true, "netcat": true, "socat": true, "telnet": true, "http": true, "https": true, "curl": true, "wget": true, "scp": true, "rsync": true, "ssh": true}

// notReaders are commands that, given a secret path, do not output its contents.
var notReaders = strings.Fields(`echo printf ls stat file find realpath basename dirname du wc ssh-keygen ssh-add`)

var secretDirs = []string{".ssh", ".aws", ".gnupg", ".config/gcloud"}

// uploadFlags are the curl and wget options whose value is sent to the server.
var uploadFlags = map[string]bool{"-d": true, "--data": true, "--data-raw": true, "--data-binary": true, "--data-urlencode": true, "-F": true, "--form": true, "-T": true, "--upload-file": true, "--post-file": true, "--body-file": true}

// valueFlags are the scp and rsync options that take a value which is not a
// file operand, such as an identity file or an exclude pattern.
var valueFlags = map[string][]string{
	"scp": strings.Fields("-i -F -o -J -P -l -c -S"),
	"rsync": strings.Fields(`-e -f --exclude --include --exclude-from --include-from --files-from --filter --password-file --rsh
--rsync-path --chmod --chown --log-file --backup-dir --partial-dir --temp-dir --compare-dest --copy-dest --link-dest`),
}

// sshKeys are the default private key file names.
var sshKeys = map[string]bool{"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ecdsa_sk": true, "id_ed25519": true, "id_ed25519_sk": true}

// SecretSends reports commands in p that send a secret file over the network:
// a secret piped or redirected into a network tool, or named as an upload.
func SecretSends(p shellparse.Pipeline) []Finding {
	var fs []Finding
	for i, c := range p.Cmds {
		if !senders[c.Name] {
			continue
		}
		secret := uploadedSecret(c)
		for _, r := range c.Redirs {
			if secret == "" && r.Op == "<" {
				secret = secretIn(r.Target)
			}
		}
		if secret == "" {
			secret = secretIn(strings.Join(c.Substituted, " "))
		}
		for _, r := range p.Cmds[:i] {
			if secret == "" && !senders[r.Name] && !slices.Contains(notReaders, r.Name) {
				secret = secretIn(strings.Join(r.Args, " "))
			}
		}
		if secret != "" {
			fs = append(fs, fnd(R3, c, "Sends your private file "+secret+" over the network. Anyone who receives it can use it to act as you. Send the public part, or a copy with the secrets removed, instead."))
		}
	}
	return fs
}

// uploadedSecret finds a secret file that c uploads by name.
func uploadedSecret(c shellparse.Command) string {
	switch c.Name {
	case "curl", "wget":
		for i, a := range c.Args {
			var val string
			if k, v, ok := strings.Cut(a, "="); ok && uploadFlags[k] {
				val = v
			} else if len(a) > 2 && a[0] == '-' && a[1] != '-' && uploadFlags[a[:2]] {
				val = a[2:] // -d@file
			} else if uploadFlags[a] && i+1 < len(c.Args) {
				val = c.Args[i+1]
			}
			if s := secretIn(val); s != "" {
				return s
			}
		}
	case "socat", "http", "https": // FILE:path, or @file in an httpie item
		return secretIn(strings.Join(c.Args, " "))
	case "scp", "rsync":
		var ops []string
		for i := 0; i < len(c.Args); i++ {
			if a := c.Args[i]; slices.Contains(valueFlags[c.Name], a) {
				i++
			} else if !strings.HasPrefix(a, "-") {
				ops = append(ops, a)
			}
		}
		if len(ops) > 1 && isRemote(ops[len(ops)-1]) {
			for _, o := range ops[:len(ops)-1] {
				if s := secretIn(o); s != "" && !isRemote(o) {
					return s
				}
			}
		}
	}
	return ""
}

// isRemote reports whether an scp or rsync operand is host:path.
func isRemote(op string) bool {
	host, _, ok := strings.Cut(op, ":")
	return ok && host != "" && !strings.Contains(host, "/")
}

// secretIn returns the first secret path named in text, or "".
func secretIn(text string) string {
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune(" \t\n\"'`();@", r) }) {
		if isSecretPath(tok) {
			return tok
		}
	}
	return ""
}

func isSecretPath(tok string) bool {
	p := Norm(tok)
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, ".pub"), base == "known_hosts":
		return false
	case base == "keys.json", strings.HasSuffix(base, ".pem"), sshKeys[base], base == ".env", strings.HasPrefix(base, ".env.") && !isEnvTemplate(base):
		return true
	}
	for _, d := range secretDirs {
		if strings.Contains("/"+p+"/", "/"+d+"/") {
			return true
		}
	}
	return false
}

// isEnvTemplate reports whether a .env.* file is an example without real values.
func isEnvTemplate(base string) bool {
	return strings.HasSuffix(base, ".example") || strings.HasSuffix(base, ".sample") || strings.HasSuffix(base, ".template")
}
