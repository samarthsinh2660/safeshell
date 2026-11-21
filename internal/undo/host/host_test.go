package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAptHistory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "history.log")
	os.WriteFile(p, []byte("Start-Date: 2025-03-01  09:00:01\nCommandline: apt install curl\nInstall: curl:amd64 (1)\n"), 0o644)
	txs, err := AptHistory(p)
	if err != nil || len(txs) != 1 || txs[0].Installed[0] != "curl:amd64" {
		t.Fatalf("%+v %v", txs, err)
	}
	if _, err := AptHistory(p + ".missing"); err == nil {
		t.Error("missing log: want an error")
	}
}

func TestExec(t *testing.T) {
	ctx := context.Background()
	if got := Exec(ctx, "echo", "active"); got != "active" {
		t.Errorf("got %q", got)
	}
	if got := Exec(ctx, "safeshell-no-such-program"); got != "" {
		t.Errorf("a missing program should print nothing, got %q", got)
	}
}

func TestUnitState(t *testing.T) {
	fake := func(answers map[string]string) Runner {
		return func(_ context.Context, name string, args ...string) string {
			if name != "systemctl" || args[1] != "nginx" {
				t.Errorf("unexpected %s %v", name, args)
			}
			return answers[args[0]]
		}
	}
	st, ok := UnitState(context.Background(), fake(map[string]string{"is-active": "active", "is-enabled": "enabled"}), "nginx")
	if !ok || st.Active != "active" || st.Enabled != "enabled" {
		t.Errorf("%+v %v", st, ok)
	}
	if _, ok := UnitState(context.Background(), fake(map[string]string{"is-active": "inactive"}), "nginx"); ok {
		t.Error("an unanswered question must not count as known")
	}
}
