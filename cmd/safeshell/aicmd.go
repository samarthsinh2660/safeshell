package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// benchBackend and loadRequests are test seams: tests replace them so a bench
// run needs neither a model nor the embedded request list.
var (
	benchBackend = buildBackend
	loadRequests = eval.LoadRequests
)

// runAI is `safeshell ai setup|status|stop`.
func runAI(args []string, stdout, stderr io.Writer) int {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell ai:", err)
		return 1
	}
	d := runtime.DefaultDirs()
	md, _ := models.Find(c.AI.Local.Model)
	switch {
	case len(args) == 1 && args[0] == "setup":
		if err := installLocal(context.Background(), c, md, stdout); err != nil {
			fmt.Fprintln(stderr, "safeshell ai setup:", err)
			return 1
		}
		fmt.Fprintf(stdout, "Ready: %s. Type English in your terminal, start a line with ?, or run  safeshell ask \"show the biggest files here\".\nOther models, or Claude / GPT / Gemini: type /models\n", md.Title)
		return 0
	case len(args) == 1 && args[0] == "stop":
		if d.Stop() {
			fmt.Fprintln(stdout, "The local AI engine was stopped. It starts again when needed.")
		} else {
			fmt.Fprintln(stdout, "The local AI engine is not running.")
		}
		return 0
	case len(args) == 1 && args[0] == "status":
		b, _ := backend()
		if b == nil {
			fmt.Fprintf(stdout, "AI: off (mode %s). %s\n", c.AI.Mode, aiOffHint)
		} else {
			fmt.Fprintf(stdout, "AI: %s (mode %s)\n", b.Name(), c.AI.Mode)
		}
		state := "not downloaded"
		if d.ModelInstalled(md) {
			state = "downloaded"
		}
		fmt.Fprintf(stdout, "Local model %s: %s\n", md.Title, state)
		if run, ok := d.Running(); ok {
			fmt.Fprintf(stdout, "Local engine: running (%s)\n", run)
		} else {
			fmt.Fprintln(stdout, "Local engine: not running (starts when needed, stops itself after 10 idle minutes)")
		}
		return 0
	}
	if len(args) >= 1 && args[0] == "bench" {
		return runBench(c, args[1:], stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return 2
}

// runBench measures how well the active model (or --model ID) turns English
// into safe, correct commands, using the labelled set in eval/requests.tsv.
func runBench(c config.Config, args []string, stdout, stderr io.Writer) int {
	var pace time.Duration
	for len(args) > 0 {
		switch {
		case len(args) >= 2 && args[0] == "--model":
			if _, ok := models.Find(args[1]); !ok {
				fmt.Fprintln(stderr, "safeshell ai bench: unknown model", args[1])
				return 2
			}
			c.AI.Mode, c.AI.Local.Model = ai.Local, args[1]
		case len(args) >= 2 && args[0] == "--pace": // a pause between requests, for cloud services with rate limits
			d, err := time.ParseDuration(args[1])
			if err != nil || d < 0 {
				fmt.Fprintln(stderr, "safeshell ai bench: --pace needs a duration such as 3s")
				return 2
			}
			pace = d
		default:
			fmt.Fprint(stderr, usage)
			return 2
		}
		args = args[2:]
	}
	b, err := benchBackend(c, runtime.DefaultDirs())
	if err != nil || b == nil {
		fmt.Fprintln(stderr, "safeshell ai bench: no AI is active.", aiOffHint)
		return 1
	}
	reqs, err := loadRequests()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell ai bench:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Testing %s on %d requests...\n", b.Name(), len(reqs))
	res := eval.RunBench(context.Background(), b, eval.BenchEnv(), reqs, pace, func(o eval.Outcome) {
		mark := "ok  "
		switch {
		case o.Skipped:
			mark = "SKIP"
		case !o.Pass:
			mark = "FAIL"
		}
		got := o.Command
		if got == "" {
			got = o.Answer
		}
		fmt.Fprintf(stdout, "%s %5.1fs  %s\n        -> %s\n", mark, o.Elapsed.Seconds(), o.Request.Text, report.Printable(got))
		if !o.Pass {
			fmt.Fprintf(stdout, "        why: %s\n", report.Printable(o.Why))
		}
	})
	var total time.Duration
	for _, o := range res.Outcomes {
		total += o.Elapsed
	}
	n := len(res.Outcomes)
	asked := res.Asked()
	fmt.Fprintf(stdout, "\n%s: %d of %d correct (%d%%), %d too dangerous, %.1fs per request on average\n",
		res.Backend, res.Passed(), asked, res.Passed()*100/max(asked, 1), res.Unsafe(), total.Seconds()/float64(max(n, 1)))
	if asked < n {
		fmt.Fprintf(stdout, "%d request(s) never reached the model (network, key or rate limit) and are not counted; try --pace 3s.\n", n-asked)
		return 1
	}
	return 0
}
