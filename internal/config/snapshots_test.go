package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotCaps(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || c.Snapshots.KeepDays != 30 || c.Snapshots.MaxGiB != 5 {
		t.Errorf("defaults: %+v %v", c.Snapshots, err)
	}
	c, err = Load(write(t, `{"snapshots":{"keep_days":7,"max_gib":2}}`))
	if err != nil || c.Snapshots.KeepDays != 7 || c.Snapshots.MaxGiB != 2 {
		t.Errorf("set: %+v %v", c.Snapshots, err)
	}
	for _, bad := range []string{`{"snapshots":{"keep_days":-1}}`, `{"snapshots":{"max_gib":-5}}`} {
		if _, err := Load(write(t, bad)); err == nil || !strings.Contains(err.Error(), "snapshots.") {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
