package providers

import "testing"

func TestProvidersAreCompleteAndFindable(t *testing.T) {
	for _, p := range All {
		if p.ID == "" || p.Title == "" || p.DefaultModel == "" || len(p.Env) == 0 || p.KeyURL == "" {
			t.Errorf("incomplete: %+v", p)
		}
		got, ok := Find(p.ID)
		if !ok || got.ID != p.ID {
			t.Errorf("Find(%s)", p.ID)
		}
		b := p.Backend("k", "")
		if b == nil || b.Name() == "" {
			t.Errorf("%s has no backend", p.ID)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Error("invented provider")
	}
}

func TestBackendNamesCarryTheModel(t *testing.T) {
	g, _ := Find("gemini")
	if n := g.Backend("k", "gemini-2.5-pro").Name(); n != "gemini:gemini-2.5-pro" {
		t.Error(n)
	}
	o, _ := Find("openai")
	if n := o.Backend("k", "").Name(); n != "gpt:gpt-6-luna" {
		t.Error(n)
	}
}

func TestKeyFromEnvChecksEachVariable(t *testing.T) {
	g, _ := Find("gemini")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", " second ")
	if g.KeyFromEnv() != "second" {
		t.Error("second variable")
	}
	t.Setenv("GEMINI_API_KEY", "first")
	if g.KeyFromEnv() != "first" {
		t.Error("first variable wins")
	}
}
