package overlayfs

import "github.com/samarthsinh2660/safeshell/internal/sandbox"

// request is what the parent sends the re-executed child on stdin.
type request struct {
	Spec   sandbox.Spec
	Shell  string
	Cwd    string
	Env    []string
	Root   string // scratch directory; the merged view is Root/merged
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
