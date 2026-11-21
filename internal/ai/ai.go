// Package ai asks a language model for help without trusting it. The model can
// explain a command, raise its risk after reading the sandbox result, or turn
// plain English into a command. It can never lower a risk level, never runs
// anything, and its answers must fit a strict schema or they are discarded.
package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/samarthsinh2660/safeshell/internal/report"
)

// Backend is one way of reaching a model.
type Backend interface {
	Name() string
	// Complete returns the model's reply to a system and user message.
	Complete(ctx context.Context, system, user string) (string, error)
}

// Mode chooses where the model runs.
type Mode string

const (
	Auto   Mode = "auto" // the default: local when its model is downloaded, otherwise off
	Off    Mode = "off"
	Local  Mode = "local"  // on this machine; nothing leaves it
	Cloud  Mode = "cloud"  // a hosted model; the command text is sent (paths redacted)
	Hybrid Mode = "hybrid" // local first, cloud when local is unavailable
)

// ErrOff means AI is switched off.
var ErrOff = errors.New("AI is off")

// ErrUnavailable means the model cannot be used on this machine (not downloaded, engine will not start), as opposed
// to being switched off or giving a bad answer.
var ErrUnavailable = errors.New("AI is not available")

// Chain tries each backend in turn and returns the first answer.
type Chain []Backend

func (c Chain) Name() string {
	var ns []string
	for _, b := range c {
		ns = append(ns, b.Name())
	}
	return strings.Join(ns, "+")
}

func (c Chain) Complete(ctx context.Context, system, user string) (string, error) {
	if len(c) == 0 {
		return "", ErrOff
	}
	var errs []error
	for _, b := range c {
		out, err := b.Complete(ctx, system, user)
		if err == nil {
			return out, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", b.Name(), err))
	}
	return "", errors.Join(errs...)
}

// minUserName is the shortest user name that is hidden as a bare word. Shorter
// names ("sam", "al", "bob") are ordinary words and names too often, so hiding
// them would mangle the text; the home path still hides them where it matters.
const minUserName = 4

// Redact replaces the home directory path with ~ and hides secret values
// (tokens, passwords, keys) and the user name before text leaves the machine.
func Redact(s string) string {
	home, _ := os.UserHomeDir()
	return redact(s, home)
}

func redact(s, home string) string {
	s = report.MaskSecrets(s)
	if len(home) <= 1 {
		return s
	}
	// The home path counts only when it ends at a path boundary, so /home/sam
	// does not eat the start of /home/samuel.
	s = regexp.MustCompile(regexp.QuoteMeta(home)+`($|[^\pL\pN_.\-])`).ReplaceAllString(s, "~$1")
	if name := path.Base(home); len(name) >= minUserName && name != "root" {
		s = regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).ReplaceAllString(s, "user")
	}
	return s
}

// redacting hides local details from a cloud backend.
type redacting struct{ Backend }

func (r redacting) Complete(ctx context.Context, system, user string) (string, error) {
	return r.Backend.Complete(ctx, system, Redact(user))
}

// Redacting wraps b so everything it is sent passes through Redact.
func Redacting(b Backend) Backend { return redacting{b} }

// Clean makes untrusted text safe to place inside a prompt: the given closing
// tags (written like "</data>") are removed whatever their case and spacing
// ("</DATA>", "< / data >"), repeatedly so a tag split around another cannot
// rebuild itself, control characters become spaces, and the text is cut to at
// most max bytes (including a trailing "...") on a character boundary.
func Clean(s string, max int, closing ...string) string {
	var tags []*regexp.Regexp
	for _, t := range closing {
		name := strings.TrimSuffix(strings.TrimPrefix(t, "</"), ">")
		tags = append(tags, regexp.MustCompile(`(?i)<\s*/\s*`+regexp.QuoteMeta(name)+`\s*>`))
	}
	s = strings.ToValidUTF8(s, "")
	for changed := true; changed; {
		changed = false
		for _, re := range tags {
			if re.MatchString(s) {
				s = re.ReplaceAllString(s, "")
				changed = true
			}
		}
	}
	s = strings.Map(func(r rune) rune {
		if report.Unsafe(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > max {
		cut := max - len("...")
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}
