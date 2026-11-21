package sandbox

// Size is what is known, before a run, about how much the command could write
// into the sandbox's scratch layer.
type Size struct {
	Bytes int64 // data found under the paths the command could touch
	// Cut says the look stopped early (too many files, too long, or already far
	// over what any place could hold), so Bytes may be short of the truth.
	Cut      bool
	RAMLimit int64 // the most the in-memory scratch layer should hold
	DiskFree int64 // free space where an on-disk scratch layer would go; 0 when there is none
}

// Placement is where a run's scratch layer goes.
type Placement int

const (
	InMemory Placement = iota // the default: a RAM-backed scratch layer
	OnDisk                    // a private folder under the user's cache
	Skip                      // too big for either: do not simulate
)

// Place decides where the scratch layer goes. Data that fits in the in-memory
// limit stays in memory, as before. A bigger run goes to disk when twice its
// size is free there (the other half is room to spare), and otherwise is not
// simulated. A look that was cut but found little is trusted: refusing to
// simulate everything in a huge folder would be worse than the small risk, and
// the in-memory area has a hard size limit that stops a run.
func Place(s Size) Placement {
	switch {
	case s.Bytes <= s.RAMLimit:
		return InMemory
	case s.Bytes <= s.DiskFree/2:
		return OnDisk
	}
	return Skip
}
