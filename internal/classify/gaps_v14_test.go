package classify

import "testing"

func TestRuleGapsV14(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		// sed e, w and W run commands and write files
		{"sed -n '1e rm -rf /' file", R4},
		{"sed 's/x/y/w /etc/passwd' file", R3},
		{"sed -n 'w /etc/passwd' file", R3},
		{"sed -n 'W out.txt' file", R2},
		{"sed -e 's/x/y/' -e '$w notes.txt' file", R2},
		{"sed --expression='/a/ w /dev/null' file", R0},
		{"sed -ne 's/x/y/e' file", R3},
		{"sed -i 's/x/y/w log.txt' file", R2},
		{"sed 's/w x/y/;y/e/w/;/e/p' file", R0},
		{"sed -f script.sed -l 70 file", R0},
		// git push in every force form
		{"git push origin +main", R3},
		{"git push origin +HEAD:main", R3},
		{"git push --mirror", R3},
		{"git push -f origin main", R3},
		{"git push origin --delete main", R3},
		{"git push origin :main", R3},
		{"git push --force-with-lease origin +main", R2},
		// sudo does not raise a redirect the user's shell opens
		{"sudo echo hi > out.txt", R2},
		{"sudo ls > /tmp/x", R2},
		{"echo x | sudo tee /etc/x", R3},
		{"sudo rm file > /tmp/log", R3},
		// pkill of root's processes or of a very short pattern
		{"pkill -9 -u root", R4},
		{"pkill -u 0 -f foo", R4},
		{"pkill --euid=root", R4},
		{"pkill -U alice,root", R4},
		{"pkill -9 -u alice", R2},
		{"pkill -u", R2},
		{"pkill sh", R3},
		{"pkill bash", R2},
		{"pkill a", R3},
		{"pkill -x vi", R2},
		{"pkill --exact sh", R2},
		{"pkill", R2},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestAlternativeIsNeverTheCommandItself(t *testing.T) {
	lines := []string{
		"git push -f origin main", "git push --force origin main", "git push origin +main", "git push origin +HEAD:main",
		"git push -fu origin main", "git push origin --delete main", "git push --mirror", "git push origin :main",
		"sed -i 's/a/b/' f", "pkill -9 -u root", "pkill a", "kill -9 1234", "killall node",
	}
	for _, l := range lines {
		for _, a := range Classify(l).Alternatives {
			if a.Command == l {
				t.Errorf("%q: alternative is the command itself", l)
			}
		}
	}
	for l, want := range map[string]string{
		"git push -f origin main":      "git push --force-with-lease origin main",
		"git push origin +main":        "git push --force-with-lease origin main",
		"git push origin +HEAD:main":   "git push --force-with-lease origin HEAD:main",
		"git push --force origin main": "git push --force-with-lease origin main",
	} {
		if v := Classify(l); len(v.Alternatives) != 1 || v.Alternatives[0].Command != want {
			t.Errorf("%q alternatives = %+v, want %q", l, v.Alternatives, want)
		}
	}
	for _, l := range []string{"git push origin --delete main", "git push --mirror", "git push origin :main"} {
		if v := Classify(l); len(v.Alternatives) != 0 {
			t.Errorf("%q should have no safer option: %+v", l, v.Alternatives)
		}
	}
}
