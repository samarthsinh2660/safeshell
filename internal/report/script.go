package report

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// ScriptFile is one checked script: the path it was given and its lines.
type ScriptFile struct {
	Path  string
	Lines []classify.LineFinding
}

type jsonLine struct {
	Line    int          `json:"line"`
	Text    string       `json:"text"`
	Level   domain.Level `json:"level"`
	Reasons []string     `json:"reasons"`
}

type jsonFile struct {
	Path  string       `json:"path"`
	Level domain.Level `json:"level"`
	Lines []jsonLine   `json:"lines"`
}

// ScriptJSON is one object per file: its path, its level and every line rated
// above R0.
func ScriptJSON(files []ScriptFile) []byte {
	out := make([]jsonFile, 0, len(files))
	for _, f := range files {
		jf := jsonFile{Path: f.Path, Level: classify.ScriptLevel(f.Lines), Lines: []jsonLine{}}
		for _, lf := range f.Lines {
			if lf.Level > domain.R0 {
				jf.Lines = append(jf.Lines, jsonLine{lf.Line, lf.Text, lf.Level, append([]string{}, lf.Reasons...)})
			}
		}
		out = append(out, jf)
	}
	b, _ := json.MarshalIndent(out, "", "  ") // plain strings and numbers cannot fail
	return b
}

// flagged is the lowest level that is reported to a code scanner or to GitHub.
const flagged = domain.R2

// annotationLevel is "error" for R3 and R4 and "warning" for R2.
func annotationLevel(l domain.Level) string {
	if l >= domain.R3 {
		return "error"
	}
	return "warning"
}

// reasonsText joins the reasons of a line into one message.
func reasonsText(lf classify.LineFinding) string {
	return fmt.Sprintf("%s: %s", lf.Level, strings.Join(lf.Reasons, "; "))
}

// ScriptSARIF is a SARIF 2.1.0 log with one result for each line rated R2 or
// above: the rule id is the level, the severity error for R3 and R4 and
// warning for R2.
func ScriptSARIF(files []ScriptFile, version string) []byte {
	type obj = map[string]any
	rules := []obj{}
	for _, l := range []domain.Level{domain.R2, domain.R3, domain.R4} {
		rules = append(rules, obj{
			"id":                   l.String(),
			"name":                 l.Label(),
			"shortDescription":     obj{"text": l.Label() + " command"},
			"defaultConfiguration": obj{"level": annotationLevel(l)},
		})
	}
	results := []obj{}
	for _, f := range files {
		uri := (&url.URL{Path: f.Path}).String()
		for _, lf := range f.Lines {
			if lf.Level < flagged {
				continue
			}
			results = append(results, obj{
				"ruleId":  lf.Level.String(),
				"level":   annotationLevel(lf.Level),
				"message": obj{"text": reasonsText(lf)},
				"locations": []obj{{"physicalLocation": obj{
					"artifactLocation": obj{"uri": uri},
					"region":           obj{"startLine": lf.Line},
				}}},
			})
		}
	}
	b, _ := json.MarshalIndent(obj{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []obj{{
			"tool": obj{"driver": obj{
				"name":           "SafeShell",
				"version":        version,
				"informationUri": "https://github.com/samarthsinh2660/safeshell",
				"rules":          rules,
			}},
			"results": results,
		}},
	}, "", "  ")
	return b
}

var (
	annotationData     = strings.NewReplacer("%", "%25") // Printable already made line breaks visible
	annotationProperty = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

// ScriptGitHub is one GitHub workflow command (::error or ::warning) for each
// line rated R2 or above, which GitHub shows on the file in a pull request.
func ScriptGitHub(files []ScriptFile) string {
	var b strings.Builder
	for _, f := range files {
		for _, lf := range f.Lines {
			if lf.Level < flagged {
				continue
			}
			fmt.Fprintf(&b, "::%s file=%s,line=%d,title=SafeShell %s::%s\n", annotationLevel(lf.Level),
				annotationProperty.Replace(f.Path), lf.Line, lf.Level, annotationData.Replace(Printable(reasonsText(lf))))
		}
	}
	return b.String()
}
