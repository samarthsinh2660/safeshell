package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFactsNameWhatTheFolderHolds(t *testing.T) {
	d := t.TempDir()
	write(t, d, ".git/HEAD", "ref: refs/heads/feat/x\n")
	write(t, d, "go.mod", "module x\n")
	write(t, d, "package.json", `{"scripts":{"test":"jest","build":"tsc"}}`)
	write(t, d, "Makefile", ".PHONY: all\nVAR := 1\nbuild: deps\n\tgo build\ntest:\n\tgo test\nbuild:\n")
	write(t, d, "Dockerfile", "FROM scratch")
	write(t, d, "Cargo.toml", "")
	write(t, d, "compose.yaml", "")
	write(t, d, "docker-compose.yml", "")
	got := strings.Join(Facts(d), "\n")
	for _, want := range []string{"git repository, branch feat/x", "Go module (go.mod)", "scripts: build test", "Makefile targets: build test\n",
		"Dockerfile present", "Cargo.toml present", "Docker Compose file: docker-compose.yml"} {
		if !strings.Contains(got+"\n", want) {
			t.Errorf("lacks %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "pyproject") || strings.Contains(got, "compose.yaml") {
		t.Errorf("%s", got)
	}
}

func TestFactsAreEmptyForAnUnrecognisedFolder(t *testing.T) {
	if got := Facts(t.TempDir()); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestBranchIsFoundFromASubfolderAndInWorktreesAndDetachedHeads(t *testing.T) {
	d := t.TempDir()
	write(t, d, "repo/.git/HEAD", "ref: refs/heads/main\n")
	write(t, d, "repo/a/b/f", "")
	if got := branch(filepath.Join(d, "repo", "a", "b")); got != "branch main" {
		t.Error(got)
	}
	write(t, d, "det/.git/HEAD", "0123456789abcdef\n")
	if got := branch(filepath.Join(d, "det")); got != "detached HEAD" {
		t.Error(got)
	}
	write(t, d, "wt/.git", "gitdir: ../real/wt\n")
	write(t, d, "real/wt/HEAD", "ref: refs/heads/topic\n")
	if got := branch(filepath.Join(d, "wt")); got != "branch topic" {
		t.Error(got)
	}
	write(t, d, "abs/.git", "gitdir: "+filepath.Join(d, "real", "wt")+"\n")
	if got := branch(filepath.Join(d, "abs")); got != "branch topic" {
		t.Error(got)
	}
	write(t, d, "odd/.git", "nonsense")
	if got := branch(filepath.Join(d, "odd")); got != "branch unknown" {
		t.Error(got)
	}
	if got := branch(d); got != "" {
		t.Error("no repository here:", got)
	}
}

func TestListsAreBoundedAndBrokenFilesIgnored(t *testing.T) {
	d := t.TempDir()
	var mk, scripts strings.Builder
	scripts.WriteString(`{"scripts":{`)
	for i := 0; i < 20; i++ {
		n := string(rune('a' + i))
		mk.WriteString("t" + n + ":\n")
		if i > 0 {
			scripts.WriteString(",")
		}
		scripts.WriteString(`"s` + n + `":"x"`)
	}
	scripts.WriteString("}}")
	write(t, d, "Makefile", mk.String())
	write(t, d, "package.json", scripts.String())
	got := strings.Join(Facts(d), "\n")
	if !strings.Contains(got, "to ...") || strings.Contains(got, "tp") || !strings.Contains(got, "sl ...") || strings.Contains(got, "sm") {
		t.Errorf("%s", got)
	}
	write(t, d, "package.json", "{not json")
	if got := strings.Join(Facts(d), "\n"); strings.Contains(got, "Node") {
		t.Errorf("%s", got)
	}
}
