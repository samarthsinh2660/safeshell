package report

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestWhyGolden(t *testing.T) {
	rm := domain.Finding{Level: domain.R3, Command: "rm -rf build", Reason: "Recursively and permanently deletes whole folders."}
	for name, tc := range map[string]struct {
		line   string
		v      domain.Verdict
		fx     []domain.Effect
		final  domain.Level
		decide domain.Decision
	}{
		"rule_sets_level": {
			"rm -rf build",
			domain.Verdict{Level: domain.R3, Findings: []domain.Finding{rm}},
			nil, domain.R3, domain.Block,
		},
		"effect_raises_level": {
			"make clean",
			domain.Verdict{Level: domain.R1, Findings: []domain.Finding{{Level: domain.R1, Command: "make clean", Reason: "Runs a build tool."}}},
			[]domain.Effect{
				{Kind: domain.EffectProcess, Evidence: domain.Inspected, Summary: "no matching process", MinLevel: domain.R0},
				{Kind: domain.EffectFS, Evidence: domain.Observed, Summary: "would delete 4,312 files (2.0 GiB)", Details: []string{"deleted /home/me/src/ (4,312 files)"}, MinLevel: domain.R3},
			},
			domain.R3, domain.Block,
		},
		"rule_wins_over_weaker_effect": {
			"rm -rf /",
			domain.Verdict{Level: domain.R4, Findings: []domain.Finding{{Level: domain.R0, Command: "rm -rf /", Reason: "Reads a file."}, {Level: domain.R4, Command: "rm -rf /", Reason: "Erases the whole system."}}},
			[]domain.Effect{{Kind: domain.EffectDisk, Evidence: domain.Previewed, Summary: "touches the root disk", MinLevel: domain.R3}},
			domain.R4, domain.HardBlock,
		},
		"unknown_command_estimated": {
			"mytool --go",
			domain.Verdict{Level: domain.R1, Findings: []domain.Finding{{Level: domain.R1, Command: "mytool --go", Reason: "Not a command SafeShell recognises yet. It will be tested in the sandbox before it runs."}}, Unclassified: []string{"mytool"}},
			[]domain.Effect{{Kind: domain.EffectReview, Evidence: domain.Estimated, Summary: "could not be tested", MinLevel: domain.R1}},
			domain.R1, domain.Confirm,
		},
		"nothing_found": {
			"ls",
			domain.Verdict{Level: domain.R0, Findings: []domain.Finding{{Level: domain.R0, Command: "ls", Reason: "Only reads."}}},
			nil, domain.R0, domain.Allow,
		},
		"piped_command_names_the_part": {
			"echo hi | sudo rm -rf x",
			domain.Verdict{Level: domain.R3, Findings: []domain.Finding{{Level: domain.R3, Command: "sudo rm -rf x", Reason: "Deletes as root."}}},
			nil, domain.R3, domain.Block,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := Why(tc.line, tc.v, tc.fx, tc.final, tc.decide)
			path := filepath.Join("testdata", "why_"+name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("output differs from %s (run: go test ./internal/report -update):\n%s", path, got)
			}
		})
	}
}

func TestWhyWithNoRuleAtAll(t *testing.T) {
	got := Why("x", domain.Verdict{Level: domain.R0}, nil, domain.R0, domain.Allow)
	for _, want := range []string{"No rule matched", "No rule set it, so it stays at R0."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
