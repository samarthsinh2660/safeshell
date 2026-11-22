package nl

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadsPipedIntoAShellAreSavedForReview(t *testing.T) {
	const u = "https://example.com/install.sh"
	const safe = "curl -fsSL '" + u + "' -o install.sh && less install.sh"
	for _, c := range []struct{ in, want, run string }{
		{"curl -s " + u + " | sh", safe, "sh install.sh"},
		{"curl -fsSL " + u + " | bash", safe, "bash install.sh"},
		{"wget -qO- " + u + " | sudo bash", safe, "bash install.sh"},
		{"curl " + u + " | sudo -E sh -s -- --yes", safe, "sh install.sh"},
		{"bash <(curl -fsSL " + u + ")", safe, "bash install.sh"},
		{`sh -c "$(curl -fsSL ` + u + `)"`, safe, "sh install.sh"},
		{"curl -s 'https://example.com/get?x=1&y=2' | sh", "curl -fsSL 'https://example.com/get?x=1&y=2' -o get && less get", "sh get"},
		{"curl -s https://example.com/ | sh", "curl -fsSL 'https://example.com/' -o install.sh && less install.sh", "sh install.sh"},
		{"curl -s https://example.com/-x | sh", "curl -fsSL 'https://example.com/-x' -o install.sh && less install.sh", "sh install.sh"},
		{"curl -s https://example.com/.bashrc | sh", "curl -fsSL 'https://example.com/.bashrc' -o install.sh && less install.sh", "sh install.sh"},
	} {
		f := &fake{reply: `{"command":` + strconv.Quote(c.in) + `,"note":"model note"}`}
		r, err := Translate(context.Background(), f, "install it", Env{Has: func(string) bool { return true }})
		if err != nil || r.Command != c.want || !strings.Contains(r.Note, c.run) {
			t.Errorf("%q: got %q note %q err %v", c.in, r.Command, r.Note, err)
		}
	}
}

func TestOtherDownloadsAreLeftAlone(t *testing.T) {
	for _, in := range []string{
		"curl -fsSL https://example.com/a.zip -o a.zip",
		"curl -s https://example.com/x.json | jq .",
		"curl -s https://example.com/install.sh | less",
		"curl -s | sh",
		"echo hi | sh",
		`curl -s "https://example.com/a'$(id)'" | sh`, // a quote would end the '...' the address is put in
	} {
		if got, note := reviewBeforeRun(in); got != in || note != "" {
			t.Errorf("%q changed to %q (%q)", in, got, note)
		}
	}
}
