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
	"strings"

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

// Redact removes the home directory and user name before text leaves the machine.
func Redact(s string) string {
	if h, err := os.UserHomeDir(); err == nil && len(h) > 1 {
		s = strings.ReplaceAll(s, h, "~")
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
// tags are removed (repeatedly, so a tag split around another cannot rebuild
// itself), control characters become spaces, and the length is bounded.
func Clean(s string, max int, closing ...string) string {
	for changed := true; changed; {
		changed = false
		for _, t := range closing {
			if strings.Contains(s, t) {
				s = strings.ReplaceAll(s, t, "")
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
		s = s[:max] + "..."
	}
	return s
}
