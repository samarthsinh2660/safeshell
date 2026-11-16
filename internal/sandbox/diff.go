// Package sandbox defines what a simulation reports. It is pure data and
// parsing (no file or process access), so the pure packages may import it.
// Reading the OverlayFS upper directory and running the command live in
// sandbox/overlayfs.
package sandbox

// Kind says what happened to a path.
type Kind string

const (
	Created  Kind = "created"
	Modified Kind = "modified"
	Deleted  Kind = "deleted"
	Replaced Kind = "replaced" // a directory emptied and recreated
)

// Change is one top-level difference. For a deleted directory, Files counts
// everything that was inside it.
type Change struct {
	Path  string // relative to the root, no leading slash
	Kind  Kind
	IsDir bool
	Files int   // files affected (1 for a plain file)
	Bytes int64 // bytes in those files
	// Notable lists up to five paths inside a deleted or replaced tree that look
	// important (git history, keys, databases, documents).
	Notable []string
}

// Abs is the change's path with its leading slash.
func (c Change) Abs() string { return "/" + c.Path }

// FileDiff is the observed effect of a command on the filesystem.
type FileDiff struct {
	Changes   []Change
	Truncated bool // a large deleted tree was counted only up to a limit
}

// Totals sums files and bytes per kind.
type Totals struct {
	Created, Modified, Deleted int
	DeletedBytes               int64
}

func (d FileDiff) Totals() Totals {
	var t Totals
	for _, c := range d.Changes {
		switch c.Kind {
		case Created:
			t.Created += c.Files
		case Modified:
			t.Modified += c.Files
		case Deleted, Replaced:
			t.Deleted += c.Files
			t.DeletedBytes += c.Bytes
		}
	}
	return t
}
