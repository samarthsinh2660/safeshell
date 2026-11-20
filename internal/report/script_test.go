package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func sampleFiles() []ScriptFile {
	return []ScriptFile{
		{Path: "deploy/up.sh", Lines: []classify.LineFinding{
			{Line: 2, Text: "ls", Level: domain.R0},
			{Line: 3, Text: "mkdir out", Level: domain.R1, Reasons: []string{"makes a folder"}},
			{Line: 5, Text: "sudo rm x", Level: domain.R2, Reasons: []string{"deletes a file"}},
			{Line: 7, Text: `rm -rf "$D"/`, Level: domain.R3, Reasons: []string{"deletes a tree", "unset: variable"}},
		}},
		{Path: "my dir/ok.sh", Lines: []classify.LineFinding{{Line: 1, Text: "pwd", Level: domain.R0}}},
	}
}

func TestScriptJSON(t *testing.T) {
	b := ScriptJSON(sampleFiles())
	var got []struct {
		Path  string
		Level string
		Lines []struct {
			Line    int
			Text    string
			Level   string
			Reasons []string
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "deploy/up.sh" || got[0].Level != "R3" || got[1].Level != "R0" {
		t.Fatalf("files: %s", b)
	}
	if n := len(got[0].Lines); n != 3 || got[0].Lines[0].Line != 3 || got[0].Lines[2].Reasons[1] != "unset: variable" {
		t.Errorf("lines above R0 only, in order: %s", b)
	}
	if !strings.Contains(string(b), `"lines": []`) {
		t.Errorf("a clean file has an empty list, not null: %s", b)
	}
}

// TestScriptSARIFShape checks the fields SARIF 2.1.0 requires and the ones GitHub code scanning reads.
func TestScriptSARIFShape(t *testing.T) {
	b := ScriptSARIF(sampleFiles(), "v1.2.3")
	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name           string `json:"name"`
					Version        string `json:"version"`
					InformationURI string `json:"informationUri"`
					Rules          []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					Physical struct {
						Artifact struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(b, &log); err != nil {
		t.Fatal(err)
	}
	if log.Version != "2.1.0" || len(log.Runs) != 1 {
		t.Fatalf("version and one run: %s", b)
	}
	run := log.Runs[0]
	d := run.Tool.Driver
	if d.Name != "SafeShell" || d.Version != "v1.2.3" || !strings.HasPrefix(d.InformationURI, "https://") || len(d.Rules) != 3 {
		t.Errorf("driver: %+v", d)
	}
	known := map[string]bool{}
	for _, r := range d.Rules {
		known[r.ID] = true
	}
	want := []struct {
		rule, level string
		line        int
	}{{"R2", "warning", 5}, {"R3", "error", 7}}
	if len(run.Results) != len(want) {
		t.Fatalf("R2 and above only: %s", b)
	}
	for i, r := range run.Results {
		loc := r.Locations[0].Physical
		if r.RuleID != want[i].rule || r.Level != want[i].level || loc.Region.StartLine != want[i].line || !known[r.RuleID] || r.Message.Text == "" || loc.Artifact.URI != "deploy/up.sh" {
			t.Errorf("result %d: %+v", i, r)
		}
	}
	if !strings.Contains(run.Results[1].Message.Text, "deletes a tree; unset: variable") {
		t.Errorf("message: %q", run.Results[1].Message.Text)
	}
}

func TestScriptSARIFEncodesThePathAndAlwaysHasResults(t *testing.T) {
	b := ScriptSARIF(sampleFiles()[1:], "dev")
	if !strings.Contains(string(b), `"results": []`) {
		t.Errorf("results is an empty list, not null: %s", b)
	}
	files := []ScriptFile{{Path: "my dir/x.sh", Lines: []classify.LineFinding{{Line: 1, Level: domain.R4, Reasons: []string{"bad"}}}}}
	b = ScriptSARIF(files, "dev")
	if !strings.Contains(string(b), `"uri": "my%20dir/x.sh"`) {
		t.Errorf("the uri is URL-encoded: %s", b)
	}
}

func TestScriptGitHub(t *testing.T) {
	got := ScriptGitHub(sampleFiles())
	want := "::warning file=deploy/up.sh,line=5,title=SafeShell R2::R2: deletes a file\n" +
		"::error file=deploy/up.sh,line=7,title=SafeShell R3::R3: deletes a tree; unset: variable\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	odd := []ScriptFile{{Path: "a,b:c%\n.sh", Lines: []classify.LineFinding{{Line: 1, Level: domain.R3, Reasons: []string{"100% bad\nreally"}}}}}
	if got := ScriptGitHub(odd); !strings.Contains(got, "file=a%2Cb%3Ac%25%0A.sh,") || !strings.Contains(got, "100%25 bad") || strings.Count(got, "\n") != 1 {
		t.Errorf("properties and message are escaped: %q", got)
	}
}
