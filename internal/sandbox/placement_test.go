package sandbox

import "testing"

func TestPlace(t *testing.T) {
	const g = 1 << 30
	for _, tc := range []struct {
		name string
		s    Size
		want Placement
	}{
		{"small stays in memory", Size{Bytes: 1 << 20, RAMLimit: 2 * g, DiskFree: 100 * g}, InMemory},
		{"exactly the limit stays in memory", Size{Bytes: 2 * g, RAMLimit: 2 * g}, InMemory},
		{"over the limit goes to disk", Size{Bytes: 3 * g, RAMLimit: 2 * g, DiskFree: 10 * g}, OnDisk},
		{"disk must hold twice the size", Size{Bytes: 3 * g, RAMLimit: 2 * g, DiskFree: 6 * g}, OnDisk},
		{"disk too small is skipped", Size{Bytes: 3 * g, RAMLimit: 2 * g, DiskFree: 5 * g}, Skip},
		{"no disk is skipped", Size{Bytes: 3 * g, RAMLimit: 2 * g}, Skip},
		{"cut and small is trusted", Size{Bytes: g, Cut: true, RAMLimit: 2 * g}, InMemory},
		{"cut and over is placed by what was found", Size{Bytes: 3 * g, Cut: true, RAMLimit: 2 * g, DiskFree: 100 * g}, OnDisk},
	} {
		if got := Place(tc.s); got != tc.want {
			t.Errorf("%s: Place(%+v) = %d, want %d", tc.name, tc.s, got, tc.want)
		}
	}
}
