// Package eval measures the classifier against a labelled corpus. The same rows
// drive the unit test and the numbers reported for the project.
package eval

import (
	"bufio"
	"embed"
	"fmt"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

//go:embed corpus/*.tsv heldout/*.tsv
var corpusFS embed.FS

// Row is one labelled command. Gap rows are known limitations: they are
// counted and reported but do not fail the test.
type Row struct {
	Want    domain.Level
	Command string
	Note    string
	Gap     bool
}

// Result is a Row together with what the classifier said.
type Result struct {
	Row
	Got domain.Level
}

// Metrics summarises one run over the corpus.
type Metrics struct {
	Total, Correct int
	Mismatches     []Result // non-gap rows where Got != Want
	SevereMisses   []Result // non-gap rows with Want >= R3 and Got < Want: the dangerous kind
	Gaps           []Result // gap rows, with what the classifier said
}

// LoadCorpus reads every corpus/*.tsv file: the set the rules were developed against.
func LoadCorpus() ([]Row, error) { return load("corpus") }

// LoadHeldout reads heldout/*.tsv: commands labelled by someone who had not
// seen the rules, which is the honest measure of how well they generalise.
func LoadHeldout() ([]Row, error) { return load("heldout") }

func load(dir string) ([]Row, error) {
	files, err := corpusFS.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, f := range files {
		fh, err := corpusFS.Open(dir + "/" + f.Name())
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(fh)
		for n := 1; sc.Scan(); n++ {
			line := sc.Text()
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				return nil, fmt.Errorf("%s:%d: want level<TAB>command", f.Name(), n)
			}
			lvl, ok := domain.ParseLevel(parts[0])
			if !ok {
				return nil, fmt.Errorf("%s:%d: bad level %q", f.Name(), n, parts[0])
			}
			r := Row{Want: lvl, Command: parts[1]}
			if len(parts) > 2 {
				r.Note = parts[2]
				r.Gap = strings.HasPrefix(r.Note, "GAP:")
			}
			rows = append(rows, r)
		}
		fh.Close()
	}
	return rows, nil
}

// Evaluate classifies every row and gathers the metrics.
func Evaluate(rows []Row) Metrics {
	var m Metrics
	for _, r := range rows {
		res := Result{Row: r, Got: classify.Classify(r.Command).Level}
		if r.Gap {
			m.Gaps = append(m.Gaps, res)
			continue
		}
		m.Total++
		if res.Got == r.Want {
			m.Correct++
			continue
		}
		m.Mismatches = append(m.Mismatches, res)
		if r.Want >= domain.R3 && res.Got < r.Want {
			m.SevereMisses = append(m.SevereMisses, res)
		}
	}
	return m
}
