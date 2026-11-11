// Package rules holds the per-tool rating rules, one file per tool family
// (fs, disk, git, infra, pkg, db, net, proc). classify calls Rate for every
// command; this package never touches the OS.
//
// Adding support for a new tool means adding a rule here, an entry in the table
// in rules.go, and rows in eval/corpus.
package rules

import (
	"path"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

var readOnly = strings.Fields(`ls ll la dir cat less more head tail wc grep egrep fgrep rg ag pwd cd echo printf date whoami id uname hostname
which type whereis file stat df du free uptime ps top htop printenv tree diff cmp sort uniq cut tr jq yq basename dirname realpath
readlink lsblk lscpu lsusb lspci lsof man help history true false test [ sleep clear export alias unalias set unset read
env nproc md5sum sha1sum sha256sum cksum column paste comm join fold nl rev tac seq yes xxd hexdump od strings
ping traceroute dig nslookup host ifconfig ip ss netstat journalctl dmesg locate fd ncdu trash-list
safeshell`) // SafeShell itself: it must never snapshot or sandbox its own undo

// devTools run project code, or edit files in ways the command line alone does not reveal.
var devTools = strings.Fields(`make cmake cargo go npm npx yarn pnpm node python python3 ruby gem gcc g++ cc clang rustc javac java mvn gradle
code vim vi nano emacs gzip gunzip bzip2 xz zcat 7z zip source . sftp ffmpeg convert pandoc trash-put trash`)

// interpreters that execute whatever is fed to them.
var interpreters = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "python": true, "python3": true, "perl": true, "ruby": true, "node": true, "source": true, ".": true}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

var table map[string]rule

func init() {
	table = map[string]rule{
		"rm": ruleRm, "unlink": fixed(R2, "Permanently removes a file."), "shred": ruleShred, "srm": ruleShred,
		"truncate": ruleTruncate,
		"mv":       ruleMv, "cp": ruleCp, "ln": ruleLn,
		"mkdir": fixed(R1, "Creates folders. Easy to undo."), "touch": fixed(R1, "Creates or touches files. Easy to undo."),
		"rmdir": fixed(R1, "Removes empty folders only."),
		"dd":    ruleDd, "mkfs": fixed(R4, "Formats a disk or partition. All data on it is destroyed."),
		"mkswap":     fixed(R4, "Formats a disk or partition as swap. All data on it is destroyed."),
		"blkdiscard": fixed(R4, "Discards every block on a device. All data on it is destroyed."),
		"wipefs":     ruleWipefs, "fdisk": ruleFdisk, "sfdisk": ruleFdisk, "gdisk": ruleFdisk, "parted": ruleParted,
		"chmod": rulePerm, "chown": rulePerm, "chgrp": rulePerm,
		"kill": ruleKill, "pkill": rulePkill, "killall": ruleKillall,
		"killall5": fixed(R4, "Kills every process on the machine."),
		"shutdown": rulePower, "reboot": rulePower, "halt": rulePower, "poweroff": rulePower, "telinit": rulePower,
		"systemctl": ruleSystemctl, "service": ruleService,
		"sed": ruleSed, "find": ruleFind, "tee": ruleTee,
		"awk": ruleAwk, "gawk": ruleAwk, "mawk": ruleAwk, "nawk": ruleAwk,
		"git": ruleGit, "docker": ruleDocker, "podman": ruleDocker, "kubectl": ruleKubectl,
		"terraform": ruleTerraform, "tofu": ruleTerraform,
		"aws": ruleCloud, "gcloud": ruleCloud, "az": ruleCloud,
		"apt": rulePkg, "apt-get": rulePkg, "dpkg": rulePkg, "dnf": rulePkg, "yum": rulePkg, "pacman": rulePkg, "zypper": rulePkg, "snap": rulePkg,
		"pip": rulePkg, "pip3": rulePkg,
		"psql": ruleDB, "mysql": ruleDB, "mariadb": ruleDB, "sqlite3": ruleDB, "mongosh": ruleDB, "redis-cli": ruleDB,
		"dropdb": fixed(R3, "Deletes a whole database permanently."), "dropuser": fixed(R3, "Deletes a database user permanently."),
		"curl": ruleCurl, "wget": ruleCurl, "rsync": ruleRsync,
		"iptables": ruleFirewall, "ip6tables": ruleFirewall, "nft": ruleFirewall, "ufw": ruleFirewall,
		"crontab":  ruleCrontab,
		"userdel":  fixed(R3, "Deletes a user account, and with options its home folder."),
		"deluser":  fixed(R3, "Deletes a user account, and with options its home folder."),
		"groupdel": fixed(R3, "Deletes a group."),
		"rmmod":    fixed(R3, "Unloads a kernel module, which can crash drivers or the system."),
		"insmod":   fixed(R3, "Loads code into the kernel."), "modprobe": fixed(R2, "Loads or unloads kernel modules."),
		"umount": fixed(R2, "Detaches a filesystem. Programs using it may fail."), "swapoff": fixed(R2, "Turns swap off, which can run the machine out of memory."),
		"mount": fixed(R1, "Attaches a filesystem."), "sysctl": ruleSysctl,
		"passwd": fixed(R2, "Changes a password."), "chroot": fixed(R2, "Runs commands inside a different root folder."),
		"visudo": fixed(R2, "Edits who is allowed to use sudo."),
		"sh":     ruleShell, "bash": ruleShell, "zsh": ruleShell, "dash": ruleShell, "ksh": ruleShell, "fish": ruleShell, "eval": ruleShell,
		"ssh": ruleSsh, "scp": fixed(R1, "Copies files to or from another machine. Existing files may be overwritten."),
		"tar": ruleTar, "unzip": ruleUnzip, "npm": ruleNpm,
	}
}

// Rate applies the rule for c's program. known is false when no rule or list
// mentions the program; it is then rated R1 and left for the sandbox to examine.
func Rate(c shellparse.Command) (fs []Finding, known bool) {
	if interpPrograms[c.Name] {
		return rateInterp(c)
	}
	c = resolveCwd(c)
	name := c.Name
	if strings.HasPrefix(name, "mkfs.") { // mkfs.ext4, mkfs.xfs, ...
		name = "mkfs"
	}
	if r, ok := table[name]; ok {
		return r(c, parseFlags(c.Args)), true
	}
	if slices.Contains(readOnly, c.Name) {
		return one(fnd(R0, c, "Only reads information.")), true
	}
	if slices.Contains(devTools, c.Name) {
		return one(fnd(R1, c, "Runs a program or edits files; effects depend on what it is given.")), true
	}
	return one(fnd(R1, c, "Not a command SafeShell recognises yet. It will be tested in the sandbox before it runs.")), false
}

// IsInterpreter reports whether name executes whatever code it is given.
func IsInterpreter(name string) bool { return interpreters[name] }

// ReadsStdin reports whether c runs a script from stdin (or a process
// substitution) instead of from a named file.
func ReadsStdin(c shellparse.Command) bool {
	if c.Downloads {
		return true
	}
	f := parseFlags(c.Args)
	if shells[c.Name] && f.has('s') { // sh -s -- args: the script still comes from stdin
		return true
	}
	return len(f.pos) == 0 || (len(f.pos) == 1 && (f.pos[0] == "-" || f.pos[0] == "/dev/stdin"))
}

// RunsFetchedCode reports whether c is handed code that a command substitution
// just downloaded: eval "$(curl ...)" or sh -c "$(curl ...)".
func RunsFetchedCode(c shellparse.Command) bool {
	if len(c.FetchedArgs) == 0 {
		return false
	}
	if c.Name == "eval" {
		return true
	}
	if !interpreters[c.Name] {
		return false
	}
	for i, a := range c.Args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.HasSuffix(a, "c") && slices.Contains(c.FetchedArgs, i+1) {
			return true
		}
	}
	return false
}

// pathCommands take file operands, so a `cd` earlier on the line changes what
// their relative operands mean.
var pathCommands = map[string]bool{"rm": true, "mv": true, "cp": true, "chmod": true, "chown": true, "chgrp": true, "shred": true, "srm": true, "truncate": true, "find": true, "ln": true, "unlink": true}

// resolveCwd rewrites relative operands against the directory the line has
// `cd`-ed to, so `cd / && rm -rf *` is rated as `rm -rf /*`.
func resolveCwd(c shellparse.Command) shellparse.Command {
	if c.Cwd == "" || c.Cwd == "?" || !pathCommands[c.Name] {
		return c
	}
	skipMode := c.Name == "chmod" || c.Name == "chown" || c.Name == "chgrp" // first operand is the mode or owner
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		switch {
		case a == "" || strings.HasPrefix(a, "-") || strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") || strings.HasPrefix(a, "$"):
			args[i] = a
		case skipMode:
			args[i] = a
			skipMode = false
		default:
			args[i] = path.Join(c.Cwd, a)
		}
	}
	c.Args = args
	return c
}
