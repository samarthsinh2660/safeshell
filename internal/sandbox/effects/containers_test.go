package effects

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// fakeTool answers a query with the text of the first key its command line
// contains, or fails like an unreachable daemon, and records every query.
type fakeTool struct {
	answers map[string]string
	calls   []string
	failAt  int // fail this query (1-based) whatever it is
}

func (f *fakeTool) run(name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if len(f.calls) == f.failAt {
		return "", errors.New("boom")
	}
	for k, v := range f.answers {
		if strings.Contains(line, k) {
			return v, nil
		}
	}
	return "", errors.New("unreachable")
}

// readOnly fails when any query is not a listing.
func (f *fakeTool) readOnly(t *testing.T) {
	t.Helper()
	for _, c := range f.calls {
		w := strings.Fields(c)
		ok := false
		for _, v := range []string{"ps", "images", "get", "container inspect", "volume ls", "volume inspect", "network ls", "system df"} {
			ok = ok || strings.Contains(strings.Join(w[1:], " "), v)
		}
		if strings.Contains(c, " rm ") || strings.Contains(c, " delete ") || strings.Contains(c, " prune") || strings.Contains(c, "dry-run") {
			ok = false
		}
		if !ok {
			t.Errorf("not a read-only query: %s", c)
		}
	}
}

func one(t *testing.T, es []domain.Effect) domain.Effect {
	t.Helper()
	if len(es) != 1 {
		t.Fatalf("want one effect, got %+v", es)
	}
	return es[0]
}

func noteOnly(t *testing.T, es []domain.Effect) {
	t.Helper()
	e := one(t, es)
	if e.Evidence != domain.Estimated || e.MinLevel != domain.R0 || !strings.Contains(e.Summary, "rule level stands") {
		t.Errorf("%+v", e)
	}
}

func TestDockerRmListsTheContainersItWouldRemove(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"container inspect": "/web running nginx:1\n/db exited postgres\n"}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker --context prod rm -f web db")))
	if e.Kind != domain.EffectContainers || e.Evidence != domain.Inspected || e.MinLevel != domain.R2 || !strings.Contains(e.Summary, "docker rm would remove 2 containers") {
		t.Errorf("%+v", e)
	}
	if d := strings.Join(e.Details, "|"); !strings.Contains(d, "container web (running, image nginx:1)") || !strings.Contains(d, "container db (exited, image postgres)") {
		t.Errorf("%q", d)
	}
	if !strings.HasPrefix(f.calls[0], "docker --context prod container inspect") || !strings.HasSuffix(f.calls[0], "web db") {
		t.Errorf("must ask the same daemon: %v", f.calls)
	}
	f.readOnly(t)
	for _, line := range []string{"docker container rm x", "docker container remove x", "docker -H tcp://h:2375 rm x", "docker --host=h rm x", "docker --config=c rm x", "docker -l debug rm x", "docker --log-level debug rm x", "docker --tls rm x"} {
		if es := InspectDocker(f.run, cmds(t, line)); len(es) != 1 || es[0].MinLevel != domain.R2 {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestDockerRmNamesNothingElseIsLeftToTheRules(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"": "x"}}
	for _, line := range []string{"docker rm", "docker rm $(docker ps -aq)", "docker rm web*", "docker rm -- -x", "docker", "docker -H", "docker container", "docker run x", "docker ps", "docker volume ls", "docker network rm x", "docker -l"} {
		if es := InspectDocker(f.run, cmds(t, line)); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	remote := []shellparse.Command{{Name: "docker", Args: []string{"rm", "x"}, Remote: "host"}}
	if es := InspectDocker(f.run, remote); es != nil || len(f.calls) != 0 {
		t.Errorf("%+v %v", es, f.calls)
	}
}

func TestDockerRmWithNothingFoundSaysSo(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"container inspect": "\n"}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker rm ghost")))
	if e.MinLevel != domain.R0 || e.Summary != "docker rm: nothing would be removed" {
		t.Errorf("%+v", e)
	}
}

func TestDockerVolumeRmSaysWhichContainersUseTheData(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"volume inspect": "data\nlogs\n", "volume=data": "db\ncache\n", "volume=logs": ""}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker volume rm data logs")))
	if e.MinLevel != domain.R3 || !strings.Contains(e.Summary, "2 volumes") {
		t.Errorf("%+v", e)
	}
	if d := strings.Join(e.Details, "|"); !strings.Contains(d, "volume data (used by db, cache)") || !strings.Contains(d, "volume logs (not used by any container)") {
		t.Errorf("%q", d)
	}
	f.readOnly(t)
}

func TestDockerVolumeRmWithManyNamesSkipsTheUsageLookups(t *testing.T) {
	var names []string
	for i := 0; i < maxVolumeLookups+1; i++ {
		names = append(names, fmt.Sprintf("v%d", i))
	}
	f := &fakeTool{answers: map[string]string{"volume inspect": strings.Join(names, "\n")}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker volume remove "+strings.Join(names, " "))))
	if len(f.calls) != 1 || e.MinLevel != domain.R3 || !strings.Contains(e.Summary, "21 volumes") {
		t.Errorf("%v %+v", f.calls, e)
	}
	if es := InspectDocker((&fakeTool{}).run, cmds(t, "docker volume rm")); es != nil {
		t.Errorf("%+v", es)
	}
}

func TestDockerPruneListsWhatCouldGo(t *testing.T) {
	f := &fakeTool{answers: map[string]string{
		"status=exited":                  "old (image app)\n",
		"dangling=true --format {{.ID}}": "abc 10MB\n",
		"images -a":                      "app:1 abc 10MB\napp:2 def 20MB\n",
		"type=custom":                    "backend\n",
		"volume ls":                      "scratch\n",
		"system df":                      "Images: 1GB (50%)\nVolumes: 2GB (100%)\n",
	}}
	cases := []struct {
		line  string
		level domain.Level
		want  string
	}{
		{"docker container prune -f", domain.R2, "docker container prune would remove up to 1 container"},
		{"docker image prune", domain.R2, "up to 1 image"},
		{"docker image prune -a --filter until=24h", domain.R2, "up to 2 images"},
		{"docker network prune", domain.R2, "up to 1 network"},
		{"docker volume prune --all", domain.R3, "up to 1 volume"},
		{"docker system prune", domain.R2, "1 container, 1 image, 1 network"},
		{"docker system prune -af --volumes", domain.R3, "1 container, 2 images, 1 network, 1 volume"},
	}
	for _, c := range cases {
		e := one(t, InspectDocker(f.run, cmds(t, c.line)))
		if e.Kind != domain.EffectContainers || e.Evidence != domain.Inspected || e.MinLevel != c.level || !strings.Contains(e.Summary, c.want) {
			t.Errorf("%s: %+v", c.line, e)
		}
	}
	f.readOnly(t)
	e := one(t, InspectDocker(f.run, cmds(t, "docker system prune")))
	if d := strings.Join(e.Details, "|"); !strings.Contains(d, "reclaimable Images: 1GB (50%)") || !strings.Contains(d, "container old (image app)") {
		t.Errorf("%q", d)
	}
}

func TestDockerPruneWithNothingToRemoveAndWithoutAReclaimableReport(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"ps": "", "images": "", "network": ""}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker system prune")))
	if e.MinLevel != domain.R0 || !strings.Contains(e.Summary, "nothing would be removed") || len(e.Details) != 0 {
		t.Errorf("%+v", e)
	}
}

func TestDockerManyObjectsAreListedButDoNotRaiseTheLevel(t *testing.T) {
	var many []string
	for i := 0; i < 12; i++ {
		many = append(many, fmt.Sprintf("c%d (image x)", i))
	}
	f := &fakeTool{answers: map[string]string{"ps": strings.Join(many, "\n")}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker container prune")))
	if e.MinLevel != domain.R2 || len(e.Details) != 11 || e.Details[10] != "... and 2 more" {
		t.Errorf("%+v", e)
	}
}

func TestDockerNamesFromTheDaemonArePrintedSafely(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"ps": "evil\x1b[31m (image x)\n"}}
	e := one(t, InspectDocker(f.run, cmds(t, "docker container prune")))
	if d := strings.Join(e.Details, "|"); strings.Contains(d, "\x1b") || !strings.Contains(d, `\x1b`) {
		t.Errorf("%q", d)
	}
}

func TestDockerThatDoesNotAnswerKeepsTheRuleLevel(t *testing.T) {
	down := &fakeTool{}
	for _, line := range []string{"docker rm web", "docker volume rm v", "docker container prune", "docker image prune", "docker network prune", "docker volume prune", "docker system prune --volumes"} {
		noteOnly(t, InspectDocker(down.run, cmds(t, line)))
	}
	// Each later query failing on its own also gives only the note.
	ps := &fakeTool{answers: map[string]string{"volume inspect": "a\n"}}
	noteOnly(t, InspectDocker(ps.run, cmds(t, "docker volume rm a")))
	for k := 1; k <= 4; k++ {
		f := &fakeTool{failAt: k, answers: map[string]string{"": "x\n"}}
		noteOnly(t, InspectDocker(f.run, cmds(t, "docker system prune -a --volumes")))
	}
	// The size report is only a bonus.
	f := &fakeTool{failAt: 5, answers: map[string]string{"": "x\n"}}
	if e := one(t, InspectDocker(f.run, cmds(t, "docker system prune -a --volumes"))); e.Evidence != domain.Inspected {
		t.Errorf("%+v", e)
	}
}

func TestToolQueryRefusesAnythingThatIsNotAListing(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"": "x"}}
	for _, tool := range []string{"docker", "kubectl"} {
		q := toolQuery{run: f.run, tool: tool}
		for _, args := range [][]string{{"rm", "x"}, {"delete", "pod", "x"}, {"system", "prune"}, {"volume", "rm", "v"}, {"container"}, {}} {
			if _, err := q.out(args...); !errors.Is(err, errNotReadOnly) {
				t.Errorf("%s %v: %v", tool, args, err)
			}
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %v", f.calls)
	}
}

func TestKubectlDeleteListsTheObjectsASelectorWouldHit(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"get": "pod/web-1\npod/web-2\n"}}
	cases := []struct{ line, query string }{
		{"kubectl delete pod web-1 web-2 -n prod", "kubectl get pod web-1 web-2 -n prod -o name --ignore-not-found"},
		{"kubectl -n prod --context stage delete deploy/web", "kubectl get deploy/web -n prod --context stage -o name"},
		{"kubectl delete pods -l app=web --all-namespaces --grace-period 0 --wait=false -o name", "kubectl get pods -l app=web --all-namespaces -o name"},
		{"kubectl delete pods --selector=app=web --namespace=prod --cascade=foreground", "kubectl get pods --selector app=web --namespace prod -o name"},
		{"kubectl delete -f deploy.yaml -R", "kubectl get -f deploy.yaml -R -o name"},
		{"kubectl delete --filename=app.yaml --as bob", "kubectl get --filename app.yaml --as bob -o name"},
		{"kubectl delete pods --all", "kubectl get pods -o name"},
		{"kubectl delete pod web --wait --cascade --force --now", "kubectl get pod web -o name"},
		{"kubectl delete pod web --wait=false --cascade=orphan --dry-run=none -o name", "kubectl get pod web -o name"},
		{"kubectl delete pod web --as-group dev --cluster c1 --user u1", "kubectl get pod web --as-group dev --cluster c1 --user u1 -o name"},
	}
	for _, c := range cases {
		f.calls = nil
		e := one(t, InspectKubectl(f.run, cmds(t, c.line)))
		if e.Kind != domain.EffectContainers || e.Evidence != domain.Inspected || e.MinLevel != domain.R2 || !strings.Contains(e.Summary, "kubectl delete would remove 2 objects") {
			t.Errorf("%s: %+v", c.line, e)
		}
		if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], c.query) {
			t.Errorf("%s: asked %v, want %s", c.line, f.calls, c.query)
		}
		f.readOnly(t)
	}
	if d := strings.Join(one(t, InspectKubectl(f.run, cmds(t, "kubectl delete pod a"))).Details, "|"); !strings.Contains(d, "object pod/web-1") {
		t.Errorf("%q", d)
	}
}

func TestKubectlDeleteOfNamespacesAndVolumesClaimsKeepData(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"get ns demo": "namespace/demo\n", "all,pvc": "deployment.apps/web\npersistentvolumeclaim/data\n"}}
	e := one(t, InspectKubectl(f.run, cmds(t, "kubectl delete ns demo")))
	if e.MinLevel != domain.R3 || !strings.Contains(e.Summary, "1 object, 2 objects inside them") {
		t.Errorf("%+v", e)
	}
	if d := strings.Join(e.Details, "|"); !strings.Contains(d, "persistentvolumeclaim/data") {
		t.Errorf("%q", d)
	}
	f.readOnly(t)
	pvc := &fakeTool{answers: map[string]string{"get": "persistentvolumeclaim/data\n"}}
	for _, line := range []string{"kubectl delete pvc data", "kubectl delete persistentvolumes/x"} {
		if e := one(t, InspectKubectl(pvc.run, cmds(t, line))); e.MinLevel != domain.R3 {
			t.Errorf("%s: %+v", line, e)
		}
	}
	nsEmpty := &fakeTool{answers: map[string]string{"get": ""}}
	if e := one(t, InspectKubectl(nsEmpty.run, cmds(t, "kubectl delete namespace/ghost"))); e.MinLevel != domain.R0 || !strings.Contains(e.Summary, "nothing would be removed") {
		t.Errorf("%+v", e)
	}
}

func TestKubectlDeleteLeavesWhatItCannotKnowToTheRules(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"": "x\n"}}
	for _, line := range []string{
		"kubectl get pods", "kubectl", "kubectl delete", "kubectl delete -n prod", "kubectl delete -f -", "kubectl delete -f=-", "kubectl delete --filename -", "kubectl delete pod a --kubeconfig /k", "kubectl delete --kubeconfig=/k pod a", "kubectl delete -k overlays/prod", "kubectl delete --kustomize=https://x/y", "kubectl delete -f https://x/y.yaml", "kubectl delete --filename=http://x/y.yaml",
		"kubectl -s https://x delete pod a", "kubectl --server=https://x delete pod a", "kubectl delete pod a --token t", "kubectl delete pod $NAME", "kubectl delete pod 'a*'",
		"kubectl apply -f x.yaml", "kubectl -n prod", "kubectl delete pod a --unknown x", "kubectl delete pod a -v 5", "kubectl delete pod a -n", "kubectl delete pod a --context",
	} {
		if es := InspectKubectl(f.run, cmds(t, line)); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	remote := []shellparse.Command{{Name: "kubectl", Args: []string{"delete", "pod", "a"}, Remote: "host"}}
	if es := InspectKubectl(f.run, remote); es != nil || len(f.calls) != 0 {
		t.Errorf("%+v %v", es, f.calls)
	}
}

func TestKubectlThatDoesNotAnswerKeepsTheRuleLevel(t *testing.T) {
	down := &fakeTool{}
	noteOnly(t, InspectKubectl(down.run, cmds(t, "kubectl delete pod a")))
	ns := &fakeTool{answers: map[string]string{"get ns demo": "namespace/demo\n"}}
	noteOnly(t, InspectKubectl(ns.run, cmds(t, "kubectl delete ns demo")))
}

func TestKubectlNamespaceContentsAreAskedFromTheSameClusterAndIdentity(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"get NS demo": "namespace/demo\n", "all,pvc": "pod/a\n"}}
	e := one(t, InspectKubectl(f.run, cmds(t, "kubectl delete NS demo --context prod --as bob --cluster c --user u --as-group g")))
	if e.MinLevel != domain.R3 || len(f.calls) != 2 {
		t.Fatalf("%+v %v", e, f.calls)
	}
	for _, w := range []string{"--context prod", "--as bob", "--cluster c", "--user u", "--as-group g"} {
		if !strings.Contains(f.calls[1], w) {
			t.Errorf("the namespace query lost %s: %s", w, f.calls[1])
		}
	}
}

func TestKubectlManyNamespacesAreNotAllLookedInto(t *testing.T) {
	var names []string
	for i := 0; i < maxNamespaceLookups+1; i++ {
		names = append(names, fmt.Sprintf("namespace/n%d", i))
	}
	f := &fakeTool{answers: map[string]string{"get ns": strings.Join(names, "\n")}}
	e := one(t, InspectKubectl(f.run, cmds(t, "kubectl delete ns --all")))
	if len(f.calls) != 1 || e.MinLevel != domain.R3 || !strings.Contains(strings.Join(e.Details, "|"), "was not listed") {
		t.Errorf("%v %+v", f.calls, e)
	}
}
