package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// missingBuildTools names the tools needed to build the speech engine that are
// not installed: "cmake", "g++" (any C++ compiler) and "make".
func (s Setup) missingBuildTools() []string {
	var missing []string
	if _, err := s.LookPath("cmake"); err != nil {
		missing = append(missing, "cmake")
	}
	if !s.hasCompiler() {
		missing = append(missing, "g++")
	}
	if _, err := s.LookPath("make"); err != nil {
		missing = append(missing, "make")
	}
	return missing
}

// packages is the Debian package list for the missing build tools, plus the
// recorder package when no recorder is installed.
func packages(missing []string, recorder bool) []string {
	pkgs := append([]string(nil), missing...)
	if !recorder {
		pkgs = append(pkgs, "pipewire-bin")
	}
	return pkgs
}

// RunTTY runs a program attached to the terminal, so sudo can ask for the password.
func RunTTY(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// EnsureTools makes sure the speech engine can be built, installing missing
// build tools with apt-get after asking. yes skips the question. ask prints the
// question and returns the answer; ok is false when there is no terminal to
// answer on or the answer could not be read. Sudo is never run unasked. A
// missing recorder is only a warning, because it is not needed to set up.
func (s Setup) EnsureTools(ctx context.Context, yes bool, ask func(question string) (answer string, ok bool)) error {
	building := s.Status().EngineBin == ""
	var missing []string
	if building {
		missing = s.missingBuildTools()
	}
	_, haveRecorder := PickRecorder(s.LookPath)
	if len(missing) > 0 {
		if err := s.installTools(ctx, yes, ask, missing, haveRecorder); err != nil {
			return err
		}
		_, haveRecorder = PickRecorder(s.LookPath)
	}
	if !haveRecorder {
		fmt.Fprintln(s.Out, "Note: no recorder is installed, so you cannot speak yet. Install one later: sudo apt-get install pipewire-bin (or pulseaudio-utils or alsa-utils).")
	}
	return nil
}

func (s Setup) installTools(ctx context.Context, yes bool, ask func(string) (string, bool), missing []string, haveRecorder bool) error {
	if _, err := s.LookPath("apt-get"); err != nil {
		return fmt.Errorf("building the speech engine needs %s. Install them with your system's package manager, then run  safeshell voice setup  again", strings.Join(missing, ", "))
	}
	args := append([]string{"apt-get", "install", "-y"}, packages(missing, haveRecorder)...)
	fmt.Fprintf(s.Out, "The speech engine needs some tools that are missing. This will run:\n  sudo %s\n", strings.Join(args, " "))
	if !yes {
		answer, ok := ask("Install them now? [Y/n] ")
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ok {
			return errors.New("could not read an answer. Run that command yourself (or run  safeshell voice setup --yes ), then run  safeshell voice setup  again")
		}
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
			return errors.New("nothing was installed. Run the command above yourself, then run  safeshell voice setup  again")
		}
	}
	if err := s.RunTTY(ctx, "sudo", args...); err != nil {
		return fmt.Errorf("installing the tools failed: %w. Try  sudo apt-get update  first, then run  safeshell voice setup  again", err)
	}
	if left := s.missingBuildTools(); len(left) > 0 {
		return fmt.Errorf("%s still missing after the install. Install them by hand, then run  safeshell voice setup  again", strings.Join(left, ", "))
	}
	return nil
}
