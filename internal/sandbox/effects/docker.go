package effects

import (
	"fmt"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// maxVolumeLookups is how many named volumes get a "which containers use it" query.
const maxVolumeLookups = 20

// InspectDocker asks the docker daemon what `docker rm`, `volume rm` and the
// prune commands would remove. It only lists (ps, images, inspect, volume ls,
// network ls, system df) and never removes anything. When docker or its daemon
// does not answer, it says so and the rule level stands.
func InspectDocker(run Runner, cmds []shellparse.Command) []domain.Effect {
	var out []domain.Effect
	for _, c := range cmds {
		if c.Name != "docker" || c.Remote != "" {
			continue
		}
		globals, words := dockerWords(c.Args)
		group, verb, rest := dockerVerb(words)
		q := toolQuery{run: run, tool: "docker", globals: globals}
		var e *domain.Effect
		var err error
		switch {
		case group == "container" && (verb == "rm" || verb == "remove"):
			e, err = q.removeContainers(rest)
		case group == "volume" && (verb == "rm" || verb == "remove"):
			e, err = q.removeVolumes(rest)
		case verb == "prune" && (group == "container" || group == "volume" || group == "image" || group == "network" || group == "system"):
			e, err = q.prune(group, parseArgs(rest))
		}
		switch {
		case err != nil:
			out = append(out, unavailable("docker"))
		case e != nil:
			out = append(out, *e)
		}
	}
	return out
}

// dockerWords splits docker's own options, which choose the daemon and are
// kept, from the words that follow them.
func dockerWords(args []string) (globals, words []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			return globals, args[i:]
		case (a == "-H" || a == "--host" || a == "--context" || a == "-c" || a == "--config") && i+1 < len(args):
			globals = append(globals, a, args[i+1])
			i++
		case strings.HasPrefix(a, "--host=") || strings.HasPrefix(a, "--context=") || strings.HasPrefix(a, "--config="):
			globals = append(globals, a)
		case (a == "-l" || a == "--log-level") && i+1 < len(args):
			i++
		}
	}
	return globals, nil
}

// dockerVerb turns `rm x`, `container rm x` and `volume prune` into a group, a
// verb and the words after them.
func dockerVerb(words []string) (group, verb string, rest []string) {
	if len(words) == 0 {
		return "", "", nil
	}
	switch words[0] {
	case "rm":
		return "container", "rm", words[1:]
	case "container", "volume", "image", "network", "system":
		if len(words) > 1 {
			return words[0], words[1], words[2:]
		}
	}
	return "", "", nil
}

// plainNames are the operands that are safe to hand to docker as they are. A
// word with $, a backtick or a wildcard is expanded by the shell first, so
// what it names is unknown here.
func plainNames(a parsedArgs) ([]string, bool) {
	names := append(append([]string{}, a.pos...), a.paths...)
	for _, n := range names {
		if strings.ContainsAny(n, "$`*?[") || strings.HasPrefix(n, "-") {
			return nil, false
		}
	}
	return names, len(names) > 0
}

func (q toolQuery) removeContainers(rest []string) (*domain.Effect, error) {
	names, ok := plainNames(parseArgs(rest))
	if !ok {
		return nil, nil
	}
	lines, err := q.out(append([]string{"container", "inspect", "--format", "{{.Name}} {{.State.Status}} {{.Config.Image}}"}, names...)...)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, l := range lines {
		if f := strings.Fields(l); len(f) == 3 {
			items = append(items, fmt.Sprintf("%s (%s, image %s)", strings.TrimPrefix(f[0], "/"), f[1], f[2]))
		}
	}
	e := removal("docker rm", []objects{{"container", items}}, false, false)
	return &e, nil
}

func (q toolQuery) removeVolumes(rest []string) (*domain.Effect, error) {
	names, ok := plainNames(parseArgs(rest))
	if !ok {
		return nil, nil
	}
	existing, err := q.out(append([]string{"volume", "inspect", "--format", "{{.Name}}"}, names...)...)
	if err != nil {
		return nil, err
	}
	items := existing
	if len(existing) <= maxVolumeLookups {
		items = nil
		for _, v := range existing {
			users, err := q.out("ps", "-a", "--filter", "volume="+v, "--format", "{{.Names}}")
			if err != nil {
				return nil, err
			}
			use := "not used by any container"
			if len(users) > 0 {
				use = "used by " + strings.Join(users, ", ")
			}
			items = append(items, fmt.Sprintf("%s (%s)", v, use))
		}
	}
	e := removal("docker volume rm", []objects{{"volume", items}}, true, false)
	return &e, nil
}

// prune lists what group's prune (or `system prune`) would remove. Docker
// keeps some of what the lists show (images a container uses, networks in
// use), so the result is an upper bound.
func (q toolQuery) prune(group string, a parsedArgs) (*domain.Effect, error) {
	all := a.short['a'] || a.long["all"]
	var groups []objects
	add := func(noun string, args ...string) error {
		lines, err := q.out(args...)
		groups = append(groups, objects{noun, lines})
		return err
	}
	var err error
	if group == "container" || group == "system" {
		err = add("container", "ps", "-a", "--filter", "status=exited", "--filter", "status=created", "--filter", "status=dead", "--format", "{{.Names}} (image {{.Image}})")
	}
	if err == nil && (group == "image" || group == "system") {
		if all {
			err = add("image", "images", "-a", "--format", "{{.Repository}}:{{.Tag}} {{.ID}} {{.Size}}")
		} else {
			err = add("image", "images", "--filter", "dangling=true", "--format", "{{.ID}} {{.Size}}")
		}
	}
	if err == nil && (group == "network" || group == "system") {
		err = add("network", "network", "ls", "--filter", "type=custom", "--format", "{{.Name}}")
	}
	if err == nil && (group == "volume" || (group == "system" && a.long["volumes"])) {
		err = add("volume", "volume", "ls", "--filter", "dangling=true", "--format", "{{.Name}}")
	}
	if err != nil {
		return nil, err
	}
	e := removal("docker "+group+" prune", groups, groups[len(groups)-1].noun == "volume" && len(groups[len(groups)-1].items) > 0, true)
	if group == "system" {
		if df, err := q.out("system", "df", "--format", "{{.Type}}: {{.Reclaimable}}"); err == nil {
			e.Details = append(e.Details, listed("reclaimable ", df)...)
		}
	}
	return &e, nil
}
