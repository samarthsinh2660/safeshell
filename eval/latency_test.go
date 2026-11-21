package eval

import (
	"sort"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// classifyP95Limit is the most a p95 classification of a safe (R0/R1) line may take. It is
// generous on purpose (the race detector in CI is several times slower): this catches a rule
// that got slow, not noise. The architecture target for the whole check is 20 ms.
const classifyP95Limit = 500 * time.Microsecond

func BenchmarkClassifyCorpus(b *testing.B) {
	rows, err := LoadCorpus()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		classify.Classify(rows[i%len(rows)].Command, "")
	}
}

func TestClassifySafeLinesP95(t *testing.T) {
	rows, err := LoadCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var samples []time.Duration
	for _, r := range rows {
		if r.Gap || r.Want > domain.R1 {
			continue
		}
		for i := 0; i < 10; i++ {
			start := time.Now()
			classify.Classify(r.Command, "")
			samples = append(samples, time.Since(start))
		}
	}
	if len(samples) == 0 {
		t.Fatal("the corpus has no R0/R1 rows")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p95 := samples[len(samples)*95/100]
	t.Logf("classify p95 over %d R0/R1 samples: %v (limit %v)", len(samples), p95, classifyP95Limit)
	if p95 > classifyP95Limit {
		t.Errorf("classify p95 is %v, over the %v limit: a classifier rule got slow", p95, classifyP95Limit)
	}
}
