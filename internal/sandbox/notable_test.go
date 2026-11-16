package sandbox

import (
	"testing"
)

func TestIsNotable(t *testing.T) {
	yes := []string{".git", "proj/.git/config", ".ssh/id_rsa", "a/id_ed25519", "x/.env", "thesis.PDF", "notes/report.docx", "data/app.sqlite3", "keys/server.pem", ".bashrc", "a/b/analysis.ipynb", "README.md"}
	no := []string{"", ".", "main.go", "build/out.o", "node_modules/x/index.js", "a.txt", "photo.jpg", "gitignore", "env"}
	for _, p := range yes {
		if !IsNotable(p) {
			t.Errorf("IsNotable(%q) = false", p)
		}
	}
	for _, p := range no {
		if IsNotable(p) {
			t.Errorf("IsNotable(%q) = true", p)
		}
	}
}
