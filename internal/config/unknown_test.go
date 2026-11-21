package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownNamesMisspelledKeys(t *testing.T) {
	p := write(t, `{"ai":{"local":{"modle":"x","gpu":"cpu"},"mode":"off"},"lern":true,"hosts":{"anything":"prod"},"voice":{"zzzzzz":1}}`)
	got := strings.Join(Unknown(p), "\n")
	for _, want := range []string{
		"unknown setting ai.local.modle is ignored: did you mean ai.local.model?",
		"unknown setting lern is ignored: did you mean learn?",
		"unknown setting voice.zzzzzz is ignored\n",
	} {
		if !strings.Contains(got+"\n", want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "hosts") || strings.Count(got, "\n") != 2 {
		t.Errorf("only the three typos should be named:\n%s", got)
	}
	// The rest of the file still applies.
	if c, err := Load(p); err != nil || c.AI.Local.GPU != "cpu" {
		t.Errorf("Load = %+v, %v", c, err)
	}
	if u := Unknown(write(t, `{"ai":{"mode":"off"}}`)); len(u) != 0 {
		t.Errorf("%v", u)
	}
	if u := Unknown(write(t, `not json`)); u != nil {
		t.Errorf("%v", u)
	}
	if u := Unknown(filepath.Join(t.TempDir(), "none.json")); u != nil {
		t.Errorf("%v", u)
	}
}

func TestLoadGPUErrorListsEveryAcceptedValue(t *testing.T) {
	_, err := Load(write(t, `{"ai":{"local":{"gpu":"tpu"}}}`))
	if err == nil || !strings.Contains(err.Error(), "auto, cuda, vulkan, cpu, on or off, not tpu") {
		t.Errorf("%v", err)
	}
}
