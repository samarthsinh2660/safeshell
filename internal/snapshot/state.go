package snapshot

import (
	"os"
	"sort"
	"time"
)

// State is the cheap fingerprint of a path (no hashing), enough to notice that
// something changed it since it was recorded.
type State struct {
	Kind  string `json:"kind"` // File, Dir, Symlink, or Absent when the path does not exist
	Mode  uint32 `json:"mode,omitempty"`
	Size  int64  `json:"size,omitempty"`
	MTime int64  `json:"mtime,omitempty"`
	Link  string `json:"link,omitempty"`
}

func observe(p string) State {
	fi, err := os.Lstat(p)
	if err != nil {
		return State{Kind: Absent}
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		l, _ := os.Readlink(p)
		return State{Kind: Symlink, Link: l}
	case fi.IsDir():
		return State{Kind: Dir} // a folder's time changes with whatever is inside it
	}
	return State{Kind: File, Mode: uint32(fi.Mode()), Size: fi.Size(), MTime: fi.ModTime().UnixNano()}
}

// record stores every entry's current state and writes the manifest.
func record(store string, m *Manifest) error {
	for i := range m.Entries {
		s := observe(m.Entries[i].Path)
		m.Entries[i].After = &s
	}
	m.Sealed = true
	return writeManifest(store, *m)
}

// sealLookback is how many of the newest snapshots Seal looks through.
const sealLookback = 20

// Seal records what the newest snapshot of this terminal looks like now, once.
// Call it when the next command in that terminal starts: the earlier command
// has finished, so this is the state it left, and Changed can tell later edits
// from it. An empty session never seals (the terminal is unknown).
func Seal(store, session string) error {
	if session == "" {
		return nil
	}
	des, err := os.ReadDir(store)
	if err != nil {
		return nil // no store yet: nothing to seal
	}
	names := make([]string, 0, len(des))
	for _, de := range des {
		names = append(names, de.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i, n := range names {
		if i == sealLookback {
			break
		}
		m, err := Load(store, n)
		if err != nil || m.Session != session {
			continue
		}
		if m.Sealed {
			return nil
		}
		return record(store, &m)
	}
	return nil
}

// Changed lists the saved paths that exist now but differ from the state
// recorded after the command ran (or after the last undo): work done since,
// which a restore would overwrite or delete. It is empty when the snapshot was
// never sealed, because then nothing can be told apart.
func Changed(m Manifest) []string {
	var out []string
	for _, e := range m.Entries {
		if e.After == nil {
			continue
		}
		if cur := observe(e.Path); cur.Kind != Absent && cur != *e.After {
			out = append(out, e.Path)
		}
	}
	return out
}

// MarkUndone notes that the snapshot was restored, and takes the restored files
// as the new baseline for Changed.
func MarkUndone(store, id string) error {
	m, err := Load(store, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	m.Undone = &now
	return record(store, &m)
}

// Pick chooses the snapshot an undo with no id should offer, from ms (newest
// first): the newest from this terminal, else from this folder, else the
// newest. Snapshots already undone are skipped. ok is false when none is left.
func Pick(ms []Manifest, o Origin) (m Manifest, ok bool) {
	var fresh []Manifest
	for _, c := range ms {
		if c.Undone == nil {
			fresh = append(fresh, c)
		}
	}
	for _, c := range fresh {
		if o.Session != "" && c.Session == o.Session {
			return c, true
		}
	}
	for _, c := range fresh {
		if o.Dir != "" && c.Dir == o.Dir {
			return c, true
		}
	}
	if len(fresh) == 0 {
		return m, false
	}
	return fresh[0], true
}
