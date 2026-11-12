package report

import (
	"slices"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func TestTouched(t *testing.T) {
	got, unsure := Touched("rm -rf *.log ~/a ./b ../c d/e plain", "/work/p", "/home/me",
		[]sandbox.Change{{Path: "tmp/x"}})
	want := []string{"/work/p", "/tmp/x", "/home/me/a", "/work/p/b", "/work/c", "/work/p/d/e"}
	if !slices.Equal(got, want) || unsure {
		t.Errorf("got %v (uncertain %v), want %v", got, unsure, want)
	}
}

// TestNotesFor runs whole lines through Touched and RelevantWarnings.
func TestNotesFor(t *testing.T) {
	docker := sandbox.Warning{Paths: []string{"/var/lib/docker"}, Text: "docker"}
	boot := sandbox.Warning{Paths: []string{"/boot"}, Text: "boot"}
	all := []sandbox.Warning{docker, boot}
	cases := []struct {
		line string
		want []string
	}{
		{"rm -rf /var/lib/*", []string{"docker"}},
		{"rm -rf /var/lib/dock*", []string{"docker"}},
		{"echo x > /boot/a", []string{"boot"}},
		{"dd if=/dev/zero of=/boot/a", []string{"boot"}},
		{"rm --dir=/boot", []string{"boot"}},
		{"rm -rf $HOME/x", []string{"docker", "boot"}},
		{`rm -rf "$D"`, []string{"docker", "boot"}},
		{"rm -rf ~root/x", []string{"docker", "boot"}},
		{"cd /etc && rm ../boot/x", []string{"boot"}},
		{"apt-get purge grub-pc", []string{"docker", "boot"}},
		{"cd sub && rm x", []string{"docker", "boot"}},
		{"echo 'unterminated", []string{"docker", "boot"}},
		{"find /var -name x | xargs rm", []string{"docker", "boot"}},
		{"cd /var/lib; rm -rf docker", []string{"docker"}},
		{"find /", []string{"docker", "boot"}},
		{"find . -name x", nil},
		{"rm -rf /home/me/proj/x", nil},
		{"rm -rf ./x", nil},
	}
	for _, c := range cases {
		touched, unsure := Touched(c.line, "/home/me/proj", "/home/me", nil)
		shown := all
		if !unsure {
			shown = RelevantWarnings(all, touched)
		}
		var got []string
		for _, w := range shown {
			got = append(got, w.Text)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.line, got, c.want)
		}
	}
}

func TestPathlessWarningAlwaysRelevant(t *testing.T) {
	ws := []sandbox.Warning{{Text: "general"}}
	if got := RelevantWarnings(ws, []string{"/tmp"}); len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestRelevantWarnings(t *testing.T) {
	docker := sandbox.Warning{Paths: []string{"/var/lib/docker"}, Text: "docker"}
	boot := sandbox.Warning{Paths: []string{"/boot"}, Text: "boot"}
	loose := sandbox.Warning{Paths: []string{"/boot", "/snap", "/var/lib", "/var", "/opt"}, Text: "loose"}
	all := []sandbox.Warning{docker, boot, loose}
	cases := []struct {
		name    string
		touched []string
		want    []string
	}{
		{"unrelated project folder", []string{"/tmp/proj"}, nil},
		{"descendant of the warning path", touchedBy("rm /var/lib/docker/x", "/tmp/p", ""), []string{"docker", "loose"}},
		{"ancestor of the warning path", []string{"/var/lib"}, []string{"docker", "loose"}},
		{"equal path", []string{"/boot"}, []string{"boot", "loose"}},
		{"home expansion", touchedBy("rm ~/f", "/tmp/p", "/opt/home"), []string{"loose"}},
		{"relative resolved against cwd", touchedBy("rm ./docker/x", "/var/lib", ""), []string{"docker", "loose"}},
		{"one of many loose paths", []string{"/opt/tool"}, []string{"loose"}},
		{"sibling with shared prefix", []string{"/bootstrap"}, nil},
		{"root touches everything", []string{"/"}, []string{"docker", "boot", "loose"}},
	}
	for _, c := range cases {
		var got []string
		for _, w := range RelevantWarnings(all, c.touched) {
			got = append(got, w.Text)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTouchedUncertainEdges(t *testing.T) {
	cases := []struct {
		name, line, cwd, home string
	}{
		{"cd with no target", "cd", "/w", "/h"},
		{"home unknown", "ls ~/x", "/w", ""},
		{"dot without a directory", "ls ..", "", "/h"},
		{"relative path without a directory", "ls a/b", "", "/h"},
		{"cd to home", "cd ~ && ls /w", "/w", "/h"},
		{"fetched argument", `sh -c "$(curl x)"`, "/w", "/h"},
		{"ssh", "ssh host rm /x", "/w", "/h"},
	}
	for _, c := range cases {
		if _, unsure := Touched(c.line, c.cwd, c.home, nil); !unsure {
			t.Errorf("%s: %q should be uncertain", c.name, c.line)
		}
	}
	if got, unsure := Touched("cd /a && ls ..", "/w", "/h", nil); unsure || !slices.Contains(got, "/") {
		t.Errorf("dot after cd: got %v, uncertain %v", got, unsure)
	}
}

func touchedBy(line, cwd, home string) []string {
	p, _ := Touched(line, cwd, home, nil)
	return p
}
