package main

import "testing"

func TestGatherEnvChecksProgramsOnThisMachine(t *testing.T) {
	e := gatherEnv(t.TempDir())
	if e.Has == nil || !e.Has("sh") || e.Has("definitely-not-a-program") {
		t.Error("Has should look on the PATH")
	}
}
