package rules

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// The home folder comes from the caller, so a home outside /home is protected
// by every rule that asks what a path is.
func TestRulesProtectTheGivenHomeFolder(t *testing.T) {
	for _, line := range []string{
		"rm -rf /srv/me",
		"chmod -R 777 /srv/me",
		"find /srv/me -delete",
		"mv /srv/me /tmp/x",
	} {
		res, err := shellparse.Parse(line)
		if err != nil {
			t.Fatal(err)
		}
		c := res.Pipelines[0].Cmds[0]
		if fs, _ := Rate(c, "/srv/me"); maxLevel(fs) != R4 {
			t.Errorf("%q with home /srv/me: %+v, want R4", line, fs)
		}
		if fs, _ := Rate(c, ""); maxLevel(fs) == R4 {
			t.Errorf("%q with no home: %+v, want below R4", line, fs)
		}
	}
}

func maxLevel(fs []Finding) domain.Level {
	var l domain.Level
	for _, f := range fs {
		l = max(l, f.Level)
	}
	return l
}
