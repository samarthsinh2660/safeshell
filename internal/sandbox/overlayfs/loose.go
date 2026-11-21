package overlayfs

import (
	"sort"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

const (
	// maxLooseFile is the biggest loose file copied into the sandbox.
	maxLooseFile = 256 << 10
	// maxLooseTotal is how much loose data is copied in all. It must stay well
	// below the size of the sandbox's scratch tmpfs, which also holds what the
	// command writes there.
	maxLooseTotal = 2 << 20
)

// looseFile is a loose file as found in a partly mirrored folder.
type looseFile struct {
	Path string
	Size int64
}

// pickLoose chooses the files that fit: each within perFile bytes and all
// together within budget bytes, smallest first so as many as possible fit. It
// returns the chosen files and the ones left out, each in a stable order.
func pickLoose(files []looseFile, perFile, budget int64) (chosen, skipped []looseFile) {
	sorted := append([]looseFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Size != sorted[j].Size {
			return sorted[i].Size < sorted[j].Size
		}
		return sorted[i].Path < sorted[j].Path
	})
	for _, f := range sorted {
		if f.Size > perFile || f.Size > budget {
			skipped = append(skipped, f)
			continue
		}
		budget -= f.Size
		chosen = append(chosen, f)
	}
	return chosen, skipped
}

// fileState is what identifies a loose file's content and metadata.
type fileState struct {
	Size  int64
	Mode  uint32
	Mtime int64
	Sum   [32]byte
}

// diffLoose compares loose files before and after a run, keyed by absolute path.
func diffLoose(before, after map[string]fileState) []sandbox.Change {
	var out []sandbox.Change
	for p, b := range before {
		a, ok := after[p]
		switch {
		case !ok:
			out = append(out, sandbox.Change{Path: p[1:], Kind: sandbox.Deleted, Files: 1, Bytes: b.Size})
		case a != b:
			out = append(out, sandbox.Change{Path: p[1:], Kind: sandbox.Modified, Files: 1, Bytes: a.Size})
		}
	}
	for p, a := range after {
		if _, ok := before[p]; !ok {
			out = append(out, sandbox.Change{Path: p[1:], Kind: sandbox.Created, Files: 1, Bytes: a.Size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
