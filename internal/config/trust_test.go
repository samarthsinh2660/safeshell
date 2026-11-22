package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

func TestTrustedRoundTripsAndKeepsTheOtherKeys(t *testing.T) {
	p := write(t, `{"learn":true,"protect":["~/thesis"],"future_key":{"a":1}}`)
	list := []policy.Trust{{Line: "rm build/x.o", Dir: "/home/me/app"}, {Line: "make clean", Dir: "/srv/a"}}
	if err := SetTrusted(p, list); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !reflect.DeepEqual(c.Trusted, list) || !c.Learn || len(c.Protect) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "future_key") {
		t.Errorf("a key this version does not know was lost: %s", b)
	}
	if err := SetTrusted(p, nil); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || len(c.Trusted) != 0 {
		t.Errorf("%+v %v", c, err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestTrustedSurvivesSave(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	var c Config
	c.Trusted = []policy.Trust{{Line: "rm build/x.o", Dir: "/app"}}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(p); err != nil || !reflect.DeepEqual(got.Trusted, c.Trusted) {
		t.Errorf("%+v %v", got, err)
	}
}

// A bad entry never trusts anything, and it does not stop the rest of the
// settings (or safeshell trust) from loading.
func TestBadTrustedEntriesAreDroppedNotFatal(t *testing.T) {
	for name, bad := range map[string]string{
		"no line":         `{"line":"","dir":"/a"}`,
		"no folder":       `{"line":"ls"}`,
		"relative folder": `{"line":"ls","dir":"app"}`,
		"unclean folder":  `{"line":"ls","dir":"/a/../b"}`,
	} {
		c, err := Load(write(t, `{"trusted":[`+bad+`,{"line":"make clean","dir":"/w"}]}`))
		if err != nil || !reflect.DeepEqual(c.Trusted, []policy.Trust{{Line: "make clean", Dir: "/w"}}) {
			t.Errorf("%s: %+v %v", name, c.Trusted, err)
		}
	}
}

func TestSetTrustedRefusesADamagedFile(t *testing.T) {
	p := write(t, `{`)
	if err := SetTrusted(p, nil); err == nil {
		t.Error("a damaged settings file must be left alone")
	}
}
