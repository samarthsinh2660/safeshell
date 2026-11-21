package overlayfs

import (
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// DiskBase is the private folder under the user's cache where a run too big
// for memory keeps its scratch layers.
func DiskBase() string {
	return filepath.Join(xdg.Dir("XDG_CACHE_HOME", ".cache"), "sandbox")
}

// request is what the parent sends the re-executed child on stdin.
type request struct {
	Spec   sandbox.Spec
	Shell  string
	Cwd    string
	Env    []string
	Root   string // scratch directory; the merged view is Root/merged
	Store  string // where extra layers get their upper and work folders: Root, or a folder on disk
	Layers []Layer
}

// outcome is what the child reports on fd 3.
type outcome struct {
	Exit  int
	Error string // setup failed; the command did not run
	// Warnings list directories that could not be overlaid and are empty in the sandbox.
	Warnings []sandbox.Warning
	// Layers are the overlays actually mounted, including ones added by the child.
	Layers []Layer
}
