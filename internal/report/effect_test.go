package report

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func TestRootEffect(t *testing.T) {
	if _, ok := RootEffect(nil); ok {
		t.Error("no root request, no effect")
	}
	e, ok := RootEffect([]string{"sudo", "su"})
	if !ok || e.Evidence != domain.Estimated || e.MinLevel != domain.R3 || !strings.Contains(e.Summary, "asks for root") {
		t.Fatalf("%+v", e)
	}
	if len(e.Details) != 1 || e.Details[0] != "tried to run sudo, su" {
		t.Errorf("details: %v", e.Details)
	}
}

func TestNetEffect(t *testing.T) {
	if _, ok := NetEffect(nil); ok {
		t.Error("no attempts, no effect")
	}
	if _, ok := NetEffect([]sandbox.NetAttempt{{Family: "unix", Addr: "/var/run/nscd/socket", Count: 4}}); ok {
		t.Error("libc's nscd probe is noise")
	}
	e, ok := NetEffect([]sandbox.NetAttempt{
		{Syscall: "connect", Family: "inet", Addr: "1.2.3.4", Port: 443, Count: 1},
		{Syscall: "connect", Family: "unix", Addr: "/var/run/docker.sock", Count: 1},
	})
	if !ok || e.Kind != domain.EffectNetwork || e.Evidence != domain.Observed || e.MinLevel != domain.R2 {
		t.Fatalf("%+v", e)
	}
	if len(e.Details) != 2 || e.Details[0] != "connect to 1.2.3.4:443" {
		t.Errorf("details: %v", e.Details)
	}
}
