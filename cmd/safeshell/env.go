package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/convo"
	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/project"
)

// usefulTools are the programs worth telling the model about when installed.
var usefulTools = strings.Fields(`git rg fd fzf jq yq curl wget tar zip unzip rsync ssh docker podman kubectl systemctl journalctl
apt dnf pacman snap pip npm node go cargo python3 make gcc ffmpeg convert trash-put tree ncdu htop lsof safeshell ip`)

// onPath reports whether a program can be found on this machine.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// gatherEnv describes this machine and directory for the assistant.
func gatherEnv(cwd string) nl.Env {
	e := nl.Env{OS: osName(), Shell: filepath.Base(os.Getenv("SHELL")), Cwd: cwd, Has: onPath, Project: project.Facts(cwd), Recent: recentCommands(),
		Conversation: conversations().Load(os.Getenv("SAFESHELL_HOOK"))}
	if des, err := os.ReadDir(cwd); err == nil {
		for _, de := range des {
			n := de.Name()
			if de.IsDir() {
				n += "/"
			}
			e.Listing = append(e.Listing, n)
		}
		sort.Strings(e.Listing)
	}
	for _, t := range usefulTools {
		if onPath(t) {
			e.Tools = append(e.Tools, t)
		}
	}
	return e
}

// conversations is where this terminal's earlier exchanges are kept; tests replace it.
var conversations = func() convo.Store { return convo.Store{Dir: convo.DefaultDir(), Now: time.Now} }

// recentCommands is what SafeShell saw in the last half hour (the audit log is
// already masked and leaves out lines that start with a space).
func recentCommands() []nl.Recent {
	var out []nl.Recent
	for _, e := range audit.Recent(auditPath(), time.Now(), 30*time.Minute, 8) {
		o := "ran"
		switch {
		case e.Outcome == "cancelled" && strings.Contains(e.Decision, "block"):
			o = "blocked"
		case e.Outcome == "cancelled":
			o = "cancelled"
		}
		out = append(out, nl.Recent{Line: e.Line, Outcome: o})
	}
	return out
}

func osName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return "Linux"
}
