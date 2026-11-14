package effects

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// maxNamespaceLookups is how many namespaces get a "what is inside" query.
const maxNamespaceLookups = 20

// kubectl options a delete may carry. Anything else after `delete` makes the
// preview give up, because an option that takes a value would otherwise be
// mistaken for an object name.
var (
	// kubectlCluster options choose the cluster and the identity, so every
	// query repeats them to ask the same cluster the delete would act on.
	kubectlCluster = map[string]bool{"--context": true, "--cluster": true, "--user": true, "--as": true, "--as-group": true}
	// kubectlNarrow options decide which objects are named; they are handed on to `kubectl get`.
	kubectlNarrow = map[string]bool{"-n": true, "--namespace": true, "-l": true, "--selector": true, "--field-selector": true,
		"-f": true, "--filename": true}
	kubectlFile     = map[string]bool{"-f": true, "--filename": true}
	kubectlPassFlag = map[string]bool{"-A": true, "--all-namespaces": true, "-R": true, "--recursive": true}
	// kubectlSkipValue take a separate value that has nothing to do with which objects are named.
	kubectlSkipValue = map[string]bool{"--grace-period": true, "--timeout": true, "-o": true, "--output": true}
	// kubectlIgnored are flags whose value, if any, must be attached (--wait=false), so they never swallow a word.
	kubectlIgnored = map[string]bool{"--all": true, "--force": true, "--now": true, "--wait": true, "--cascade": true, "--ignore-not-found": true,
		"--dry-run": true, "--interactive": true}
	// kubectlOtherCluster options point at a server, credential or kubeconfig by
	// hand, or fetch a remote base (kustomize); the preview would repeat a secret
	// or reach out before you approved anything, so it is skipped.
	kubectlOtherCluster = map[string]bool{"-s": true, "--server": true, "--token": true, "--kubeconfig": true, "-k": true, "--kustomize": true}
	kubectlNamespaces   = map[string]bool{"namespace": true, "namespaces": true, "ns": true}
	kubectlData         = map[string]bool{"pvc": true, "persistentvolumeclaim": true, "persistentvolumeclaims": true, "pv": true, "persistentvolume": true, "persistentvolumes": true}
)

// kubectlDelete is a delete as far as the preview can read it.
type kubectlDelete struct {
	targets []string // the resource type and names after delete
	flags   []string // options that narrow the objects, and the cluster ones, for `kubectl get`
	cluster []string // only the options that choose the cluster and identity
}

// InspectKubectl lists the objects a `kubectl delete` names, using a plain
// read-only `kubectl get -o name` with the same selector, namespace and files.
// It never sends a delete, not even a dry run. When kubectl or the cluster does
// not answer, it says so and the rule level stands. Note that credential
// plugins in the kubeconfig may run during the query, as they would for the
// command itself.
func InspectKubectl(run Runner, cmds []shellparse.Command) []domain.Effect {
	var out []domain.Effect
	for _, c := range cmds {
		if c.Name != "kubectl" || c.Remote != "" {
			continue
		}
		d, ok := parseKubectlDelete(c.Args)
		if !ok {
			continue
		}
		e, err := toolQuery{run: run, tool: "kubectl"}.deleteTargets(d)
		if err != nil {
			e = unavailable("kubectl")
		}
		out = append(out, e)
	}
	return out
}

// parseKubectlDelete reads a delete. ok is false for any other command, for a
// delete whose objects cannot be known here (standard input, an option this
// does not know, a shell-expanded word) and for one that names nothing.
func parseKubectlDelete(args []string) (d kubectlDelete, ok bool) {
	deleting, files, stdin := false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		key, val, attached := strings.Cut(a, "=")
		take := func() { // forward an option and its value
			if !attached && i+1 < len(args) {
				val = args[i+1]
				i++
			}
			d.flags = append(d.flags, key, val)
		}
		switch {
		case kubectlOtherCluster[key]:
			return d, false
		case kubectlCluster[key] || kubectlNarrow[key]:
			if !attached && i+1 >= len(args) {
				return d, false
			}
			take()
			if kubectlCluster[key] {
				d.cluster = append(d.cluster, key, val)
			}
			if kubectlFile[key] {
				files = true
				stdin = stdin || val == "-"
				if strings.Contains(val, "://") {
					return d, false // a manifest at a web address is fetched, which a preview must not do
				}
			}
		case kubectlPassFlag[a]:
			d.flags = append(d.flags, a)
		case kubectlSkipValue[key]:
			if !attached {
				i++
			}
		case kubectlIgnored[key]:
		case strings.HasPrefix(a, "-"):
			return d, false // an option this does not know may take a value
		case !deleting:
			if a != "delete" {
				return d, false
			}
			deleting = true
		case strings.ContainsAny(a, "$`*?["):
			return d, false
		default:
			d.targets = append(d.targets, a)
		}
	}
	// Objects read from standard input are not known here.
	return d, deleting && !stdin && (len(d.targets) > 0 || files)
}

func (q toolQuery) deleteTargets(d kubectlDelete) (domain.Effect, error) {
	args := append(append([]string{"get"}, d.targets...), d.flags...)
	names, err := q.out(append(args, "-o", "name", "--ignore-not-found")...)
	if err != nil {
		return domain.Effect{}, err
	}
	groups := []objects{{"object", names}}
	keepsData, isNamespace := false, false
	if len(d.targets) > 0 {
		kind := strings.ToLower(strings.SplitN(d.targets[0], "/", 2)[0])
		keepsData = kubectlData[kind]
		isNamespace = kubectlNamespaces[kind]
	}
	var notes []string
	if isNamespace {
		keepsData = true
		if len(names) > maxNamespaceLookups {
			notes = append(notes, "what is inside these namespaces was not listed, because there are many")
		} else {
			var inside []string
			for _, n := range names {
				lines, err := q.out(append([]string{"get", "all,pvc,configmap,secret", "-n", strings.TrimPrefix(n, "namespace/"), "-o", "name", "--ignore-not-found"}, d.cluster...)...)
				if err != nil {
					return domain.Effect{}, err
				}
				inside = append(inside, lines...)
			}
			groups = append(groups, objects{"object inside them", inside})
		}
	}
	e := removal("kubectl delete", groups, keepsData, false)
	e.Details = append(e.Details, notes...)
	return e, nil
}
