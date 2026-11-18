package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCreateAtRecordsOriginAndOldManifestsStayReadable(t *testing.T) {
	store, f := t.TempDir(), filepath.Join(t.TempDir(), "a")
	write(t, f, "x", 0o644)
	m, err := CreateAt(store, Origin{Dir: "/w", Session: "7"}, "rm a", []Target{{Path: f}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Load(store, m.ID)
	if got.Dir != "/w" || got.Session != "7" || got.Sealed || got.Undone != nil {
		t.Errorf("%+v", got)
	}
	old := "20200101T000000.000000000Z"
	write(t, filepath.Join(store, old, "manifest.json"), `{"id":"`+old+`","created":"2020-01-01T00:00:00Z","line":"rm b","bytes":0,"entries":[]}`, 0o600)
	if m, err := Load(store, old); err != nil || m.Dir != "" || m.Session != "" || m.Undone != nil {
		t.Errorf("old manifest: %+v %v", m, err)
	}
}

func TestSealThenChangedTellsLaterEditsFromWhatTheCommandLeft(t *testing.T) {
	store, root := t.TempDir(), t.TempDir()
	mod, gone, made := filepath.Join(root, "mod"), filepath.Join(root, "gone"), filepath.Join(root, "made")
	link, dir := filepath.Join(root, "link"), filepath.Join(root, "d")
	write(t, mod, "v1", 0o644)
	write(t, gone, "v1", 0o644)
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(dir, 0o755)
	m, err := CreateAt(store, Origin{Session: "7"}, "x", []Target{{Path: mod}, {Path: gone}, {Path: made, Create: true}, {Path: link}, {Path: dir}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := Changed(m); len(got) != 0 {
		t.Errorf("unsealed snapshot reports %v", got)
	}
	// The command runs.
	write(t, mod, "version two", 0o644)
	os.Remove(gone)
	write(t, made, "new", 0o644)
	if err := Seal(store, "7"); err != nil {
		t.Fatal(err)
	}
	m, _ = Load(store, m.ID)
	if !m.Sealed || len(Changed(m)) != 0 {
		t.Fatalf("right after the command: sealed %v changed %v", m.Sealed, Changed(m))
	}
	// Later work: an edit, a file that came back, a created file removed (not newer work), a swapped link.
	write(t, mod, "v3 mine", 0o644)
	write(t, gone, "back", 0o644)
	os.Remove(made)
	os.Remove(link)
	os.Symlink("elsewhere", link)
	got := Changed(m)
	if want := []string{gone, link, mod}; !reflect.DeepEqual(got, want) { // the folder's own time does not count
		t.Errorf("changed %v, want %v", got, want)
	}
}

func TestSealOnlyTouchesThisTerminalsNewestSnapshotOnce(t *testing.T) {
	store, f := t.TempDir(), filepath.Join(t.TempDir(), "a")
	write(t, f, "x", 0o644)
	mine, _ := CreateAt(store, Origin{Session: "1"}, "mine", []Target{{Path: f}}, 0)
	other, _ := CreateAt(store, Origin{Session: "2"}, "other", []Target{{Path: f}}, 0)
	old, _ := Create(store, "old", []Target{{Path: f}}, 0)
	write(t, filepath.Join(store, "garbage", "manifest.json"), "{", 0o600)
	for _, session := range []string{"", "9"} {
		if err := Seal(store, session); err != nil {
			t.Fatal(err)
		}
	}
	if err := Seal(t.TempDir()+"/none", "1"); err != nil { // no store yet
		t.Fatal(err)
	}
	for _, m := range []Manifest{mine, other, old} {
		if got, _ := Load(store, m.ID); got.Sealed {
			t.Errorf("%s sealed by the wrong terminal", m.Line)
		}
	}
	if err := Seal(store, "1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(store, mine.ID); !got.Sealed {
		t.Error("own snapshot not sealed")
	}
	if got, _ := Load(store, other.ID); got.Sealed {
		t.Error("another terminal's snapshot sealed")
	}
	write(t, f, "changed after", 0o644) // a second Seal must not re-record
	if err := Seal(store, "1"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(store, mine.ID)
	if len(Changed(got)) != 1 {
		t.Errorf("sealed twice: %v", Changed(got))
	}
}

func TestSealReadsOnlyThisTerminalsNote(t *testing.T) {
	store, f := t.TempDir(), filepath.Join(t.TempDir(), "a")
	write(t, f, "x", 0o644)
	first, _ := CreateAt(store, Origin{Session: "1"}, "first", []Target{{Path: f}}, 0)
	for i := 0; i < 30; i++ { // other terminals' snapshots do not get in the way
		CreateAt(store, Origin{Session: "2"}, "busy", []Target{{Path: f}}, 0)
	}
	if err := Seal(store, "1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(store, first.ID); !got.Sealed {
		t.Error("this terminal's snapshot was not sealed")
	}
	// A note that names a missing snapshot, or one from another terminal, is dropped.
	for _, id := range []string{"20000101T000000.000000000Z", first.ID} {
		write(t, filepath.Join(store, ".unsealed-3"), id, 0o600)
		if err := Seal(store, "3"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(store, ".unsealed-3")); err == nil {
			t.Errorf("note naming %s was kept", id)
		}
	}
	if ms, _ := List(store); len(ms) != 31 {
		t.Errorf("the notes must not be listed as snapshots: %d", len(ms))
	}
}

func TestMarkUndoneRecordsWhenAndResetsTheBaseline(t *testing.T) {
	store, f := t.TempDir(), filepath.Join(t.TempDir(), "a")
	write(t, f, "v1", 0o644)
	m, _ := Create(store, "x", []Target{{Path: f}}, 0)
	write(t, f, "v2", 0o644)
	if _, err := Restore(store, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := MarkUndone(store, m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(store, m.ID)
	if got.Undone == nil || !got.Sealed || len(Changed(got)) != 0 {
		t.Fatalf("%+v", got)
	}
	write(t, f, "v3 later", 0o644)
	if len(Changed(got)) != 1 {
		t.Error("an edit after the undo was not noticed")
	}
	if err := MarkUndone(store, "bad"); err == nil {
		t.Error("expected an error for a bad id")
	}
}

func TestPick(t *testing.T) {
	when := time.Now()
	ms := []Manifest{ // newest first
		{ID: "4", Session: "2", Dir: "/b", Undone: &when},
		{ID: "3", Session: "2", Dir: "/b"},
		{ID: "2", Session: "1", Dir: "/a"},
		{ID: "1", Session: "1", Dir: "/a"},
	}
	for _, tc := range []struct {
		name string
		o    Origin
		want string
	}{
		{"this terminal beats a newer one", Origin{Session: "1", Dir: "/b"}, "2"},
		{"used snapshots are skipped", Origin{Session: "2"}, "3"},
		{"this folder when the terminal is unknown", Origin{Session: "9", Dir: "/a"}, "2"},
		{"otherwise the newest unused", Origin{Session: "9", Dir: "/c"}, "3"},
		{"nothing known at all", Origin{}, "3"},
	} {
		if got, ok := Pick(ms, tc.o); !ok || got.ID != tc.want {
			t.Errorf("%s: %q %v, want %q", tc.name, got.ID, ok, tc.want)
		}
	}
	if _, ok := Pick(ms[:1], Origin{}); ok {
		t.Error("an undone snapshot was offered")
	}
	if _, ok := Pick(nil, Origin{}); ok {
		t.Error("offered from nothing")
	}
}
