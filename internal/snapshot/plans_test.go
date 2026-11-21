package snapshot

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/undo"
)

func TestAddPlansAreKeptWithTheSnapshot(t *testing.T) {
	store := t.TempDir()
	m, err := CreateAt(store, Origin{}, "systemctl stop nginx", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddPlans(store, m.ID, []undo.Plan{{Command: "sudo systemctl start nginx", Explain: "starts it"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(store, m.ID)
	if len(got.Plans) != 1 || got.Plans[0].Command != "sudo systemctl start nginx" {
		t.Errorf("plans %+v", got.Plans)
	}
	if err := AddPlans(store, "20000101T000000.000000000Z", nil); err == nil {
		t.Error("unknown snapshot: want an error")
	}
}
