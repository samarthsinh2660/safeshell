package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// modelMenu is the /models screen. Its effects are injected so tests do not
// download gigabytes or call paid APIs.
type modelMenu struct {
	in      *bufio.Reader
	out     io.Writer
	cfgPath string
	dirs    runtime.Dirs
	install func(ctx context.Context, cfg config.Config, m models.Model, w io.Writer) error
	check   func(p providers.Provider, key, model string) error
	secret  func(prompt string) (string, error)
	list    func(ctx context.Context, p providers.Provider, key string) ([]ai.ModelInfo, error)
}

func newModelMenu(in io.Reader, out io.Writer) *modelMenu {
	return &modelMenu{
		in: bufio.NewReader(in), out: out, cfgPath: config.Path(), dirs: runtime.DefaultDirs(),
		install: installLocal,
		check:   func(p providers.Provider, key, model string) error { return providers.Check(p.Backend(key, model)) },
		secret:  readSecret,
		list:    listCloud,
	}
}

// installLocal downloads the best engine for this machine (and the CPU one as
// a fallback) and the model.
func installLocal(ctx context.Context, cfg config.Config, m models.Model, w io.Writer) error {
	d := runtime.DefaultDirs()
	if err := d.InstallPreferred(ctx, nil, goruntime.GOARCH, cfg.AI.Local.GPU, w); err != nil {
		return err
	}
	return d.InstallModel(ctx, nil, m, w)
}

// listCloud asks a provider which models the key can use.
func listCloud(ctx context.Context, p providers.Provider, key string) ([]ai.ModelInfo, error) {
	return p.ListModels(ctx, key)
}

func readSecret(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", errors.New("no terminal to read the key from; set the key in your environment instead")
	}
	defer tty.Close()
	fmt.Fprint(tty, prompt)
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	return strings.TrimSpace(string(b)), err
}

func (m *modelMenu) line(prompt string) (string, bool) {
	fmt.Fprint(m.out, prompt)
	s, err := m.in.ReadString('\n')
	return strings.TrimSpace(s), err == nil || s != ""
}

func (m *modelMenu) render(c config.Config) {
	fmt.Fprintf(m.out, "\nSafeShell AI models        mode: %s\n", c.AI.Mode)
	fmt.Fprintln(m.out, "\n  On this machine (private, free, works offline)")
	for i, md := range models.Catalog {
		state := "not downloaded"
		if m.dirs.ModelInstalled(md) {
			state = "downloaded"
		}
		mark := ""
		if md.ID == c.AI.Local.Model && (c.AI.Mode == ai.Local || c.AI.Mode == ai.Auto || c.AI.Mode == ai.Hybrid) {
			mark = "  <- in use"
		}
		fmt.Fprintf(m.out, "  %d) %-20s %6s  %-14s %s%s\n", i+1, md.Title, report.HumanBytes(md.Size), state, md.Note, mark)
	}
	fmt.Fprintln(m.out, "\n  In the cloud (needs your API key; the command text is sent, your home folder name hidden)")
	for i, p := range providers.All {
		key := "no key yet"
		if config.Key(p) != "" {
			key = "key saved"
		}
		model := p.DefaultModel
		if c.AI.Cloud.Provider == p.ID && c.AI.Cloud.Model != "" {
			model = c.AI.Cloud.Model
		}
		mark := ""
		if c.AI.Cloud.Provider == p.ID && (c.AI.Mode == ai.Cloud || c.AI.Mode == ai.Hybrid) {
			mark = "  <- in use"
		}
		fmt.Fprintf(m.out, "  %d) %-7s %-10s %-18s %s%s\n", len(models.Catalog)+i+1, p.Title, "("+p.Maker+")", model, key, mark)
	}
	fmt.Fprintln(m.out, "\n  A number picks (and downloads or connects) it.   key N: change a key   model N NAME: change a cloud model   choose N: pick from the models your key offers")
	fmt.Fprintln(m.out, "  off / auto / local / cloud / hybrid: change the mode.   q: leave")
}

// Run shows the menu until the person leaves.
func (m *modelMenu) Run(ctx context.Context) {
	for {
		c, err := config.Load(m.cfgPath)
		if err != nil {
			fmt.Fprintln(m.out, "Cannot read the settings:", err)
			return
		}
		m.render(c)
		in, ok := m.line("\n> ")
		if !ok || in == "" || in == "q" || in == "quit" {
			return
		}
		m.handle(ctx, c, strings.Fields(in))
	}
}

func (m *modelMenu) handle(ctx context.Context, c config.Config, f []string) {
	switch f[0] {
	case "off", "auto", "local", "cloud", "hybrid":
		c.AI.Mode = ai.Mode(f[0])
		m.save(c, "Mode is now "+f[0]+".")
		return
	case "key":
		if p, ok := m.cloudAt(f); ok {
			m.connect(ctx, c, p, true)
		}
		return
	case "choose":
		if p, ok := m.cloudAt(f); ok {
			m.choose(ctx, c, p)
		}
		return
	case "model":
		if p, ok := m.cloudAt(f); ok && len(f) == 3 {
			c.AI.Cloud.Provider, c.AI.Cloud.Model = p.ID, f[2]
			m.save(c, p.Title+" will use the model "+f[2]+".")
		} else if ok {
			fmt.Fprintln(m.out, "Usage: model N NAME   (for example: model 6 gpt-6-luna)")
		}
		return
	}
	n, err := strconv.Atoi(f[0])
	switch {
	case err != nil || n < 1 || n > len(models.Catalog)+len(providers.All):
		fmt.Fprintln(m.out, "Please type one of the numbers, a mode, or q.")
	case n <= len(models.Catalog):
		m.useLocal(ctx, c, models.Catalog[n-1])
	default:
		m.connect(ctx, c, providers.All[n-len(models.Catalog)-1], false)
	}
}

// cloudAt reads the cloud model number in f[1].
func (m *modelMenu) cloudAt(f []string) (providers.Provider, bool) {
	if len(f) >= 2 {
		if n, err := strconv.Atoi(f[1]); err == nil {
			if k := n - len(models.Catalog) - 1; k >= 0 && k < len(providers.All) {
				return providers.All[k], true
			}
		}
	}
	fmt.Fprintf(m.out, "That needs a cloud model number (%d to %d).\n", len(models.Catalog)+1, len(models.Catalog)+len(providers.All))
	return providers.Provider{}, false
}

func (m *modelMenu) useLocal(ctx context.Context, c config.Config, md models.Model) {
	if !m.dirs.ModelInstalled(md) {
		a, _ := m.line(fmt.Sprintf("Download %s (%s)? [Y/n] ", md.Title, report.HumanBytes(md.Size)))
		if a != "" && !strings.EqualFold(a, "y") && !strings.EqualFold(a, "yes") {
			return
		}
		if err := m.install(ctx, c, md, m.out); err != nil {
			fmt.Fprintln(m.out, "Download failed:", err)
			return
		}
	}
	c.AI.Local.Model = md.ID
	if c.AI.Mode != ai.Hybrid {
		c.AI.Mode = ai.Local
	}
	m.save(c, md.Title+" is ready and in use.")
}

func (m *modelMenu) connect(ctx context.Context, c config.Config, p providers.Provider, replace bool) {
	key := config.Key(p)
	if key == "" || replace {
		fmt.Fprintf(m.out, "Create a key at %s\n", p.KeyURL)
		k, err := m.secret("Paste your " + p.Title + " API key (hidden): ")
		if err != nil || len(k) < 10 || strings.ContainsAny(k, " \t") {
			fmt.Fprintln(m.out, "That does not look like an API key; nothing was saved.")
			return
		}
		fmt.Fprintln(m.out, "Checking the key...")
		model := ""
		if c.AI.Cloud.Provider == p.ID { // a model name set for another provider would not exist here
			model = c.AI.Cloud.Model
		}
		if err := m.check(p, k, model); err != nil {
			fmt.Fprintln(m.out, "The key did not work:", err)
			return
		}
		if err := config.SaveKey(p, k); err != nil {
			fmt.Fprintln(m.out, "Could not save the key:", err)
			return
		}
	}
	if c.AI.Cloud.Provider != p.ID {
		c.AI.Cloud.Model = ""
	}
	c.AI.Cloud.Provider = p.ID
	if c.AI.Mode != ai.Hybrid {
		c.AI.Mode = ai.Cloud
	}
	m.save(c, p.Title+" ("+p.Maker+") is connected and in use.")
}

// current is the cloud model in use for p: the saved one, or the provider's default.
func current(c config.Config, p providers.Provider) string {
	if c.AI.Cloud.Provider == p.ID && c.AI.Cloud.Model != "" {
		return c.AI.Cloud.Model
	}
	return p.DefaultModel
}

// choose lists the models p offers this key and saves the one the person picks.
func (m *modelMenu) choose(ctx context.Context, c config.Config, p providers.Provider) {
	key := config.Key(p)
	if key == "" {
		fmt.Fprintf(m.out, "There is no %s key yet. Type the number of %s first to connect it.\n", p.Title, p.Title)
		return
	}
	list, err := m.list(ctx, p, key)
	if err != nil {
		fmt.Fprintf(m.out, "Could not list the %s models: %v\nThe model stays %s.\n", p.Title, err, current(c, p))
		return
	}
	if len(list) == 0 {
		fmt.Fprintf(m.out, "%s offered no models for this key. The model stays %s.\n", p.Title, current(c, p))
		return
	}
	fmt.Fprintf(m.out, "\nModels your %s key can use:\n", p.Title)
	for i, md := range list {
		mark, name := "", ""
		if md.ID == current(c, p) {
			mark = "  <- in use"
		}
		if md.Name != "" {
			name = "  (" + md.Name + ")"
		}
		fmt.Fprintf(m.out, "  %d) %s%s%s\n", i+1, md.ID, name, mark)
	}
	in, _ := m.line("Pick a number (Enter keeps the current model): ")
	n, err := strconv.Atoi(in)
	if err != nil || n < 1 || n > len(list) {
		fmt.Fprintf(m.out, "No change. The model stays %s.\n", current(c, p))
		return
	}
	c.AI.Cloud.Provider, c.AI.Cloud.Model = p.ID, list[n-1].ID
	m.save(c, p.Title+" will use the model "+list[n-1].ID+".")
}

func (m *modelMenu) save(c config.Config, msg string) {
	if err := config.Save(m.cfgPath, c); err != nil {
		fmt.Fprintln(m.out, "Could not save the settings:", err)
		return
	}
	fmt.Fprintln(m.out, msg)
}

// runModelsList is `safeshell models list`: the models the active cloud provider offers.
func runModelsList(ctx context.Context, stdout, stderr io.Writer, list func(context.Context, providers.Provider, string) ([]ai.ModelInfo, error)) int {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell models:", err)
		return 1
	}
	p, ok := providers.Find(c.AI.Cloud.Provider)
	if !ok {
		fmt.Fprintln(stderr, "safeshell models: no cloud provider is set. Type /models to connect Claude, GPT or Gemini.")
		return 1
	}
	key := config.Key(p)
	if key == "" {
		fmt.Fprintf(stderr, "safeshell models: there is no %s key yet. Type /models to add one.\n", p.Title)
		return 1
	}
	models, err := list(ctx, p, key)
	if err != nil {
		fmt.Fprintf(stderr, "safeshell models: could not list the %s models: %v\n", p.Title, err)
		return 1
	}
	for _, md := range models {
		line := md.ID
		if md.Name != "" {
			line += "  (" + md.Name + ")"
		}
		if md.ID == current(c, p) {
			line += "  <- in use"
		}
		fmt.Fprintln(stdout, line)
	}
	return 0
}

// runModels is `safeshell models` and the /models command.
func runModels(in io.Reader, out io.Writer) int {
	newModelMenu(in, out).Run(context.Background())
	return 0
}

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
			fmt.Fprintf(stdout, "AI: off (mode %s). Type /models to choose a model.\n", c.AI.Mode)
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
	b, err := buildBackend(c, runtime.DefaultDirs())
	if err != nil || b == nil {
		fmt.Fprintln(stderr, "safeshell ai bench: no AI is active. Type /models to choose one.")
		return 1
	}
	reqs, err := eval.LoadRequests()
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

// doctorAI says which engine the local AI would use on this machine and what is installed.
func doctorAI(w io.Writer) {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		fmt.Fprintln(w, "\nAI settings are broken:", err)
		return
	}
	d := runtime.DefaultDirs()
	fmt.Fprintf(w, "\nLocal AI (mode %s, gpu setting %s)\n", c.AI.Mode, c.AI.Local.GPU)
	if drv := runtime.NvidiaDriver(); drv > 0 {
		fmt.Fprintf(w, "  info     %-26s driver %d\n", "NVIDIA graphics card", drv)
	} else {
		fmt.Fprintf(w, "  info     %-26s none found\n", "NVIDIA graphics card")
	}
	fmt.Fprintf(w, "  info     %-26s %v\n", "Vulkan loader", runtime.HasVulkan())
	var order, have []string
	for _, b := range runtime.Candidates(goruntime.GOARCH, c.AI.Local.GPU) {
		order = append(order, b.Variant)
		if d.EngineInstalled(b) {
			have = append(have, b.Variant)
		}
	}
	if len(order) == 0 {
		fmt.Fprintf(w, "  MISSING  %-26s none for %s: use a cloud model\n", "local engine", goruntime.GOARCH)
		return
	}
	fmt.Fprintf(w, "  info     %-26s %s (best first)\n", "engines it would try", strings.Join(order, ", "))
	fmt.Fprintf(w, "  info     %-26s %s\n", "engines installed", orNone(have))
	md, _ := models.Find(c.AI.Local.Model)
	fmt.Fprintf(w, "  info     %-26s %s, downloaded: %v\n", "model", md.Title, d.ModelInstalled(md))
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none yet (run: safeshell ai setup)"
	}
	return strings.Join(s, ", ")
}
