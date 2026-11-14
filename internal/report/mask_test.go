package report

import "testing"

func TestMaskSecrets(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"ls -la", "ls -la"},
		{"export TOKEN=abc123", "export TOKEN=***"},
		{"GITHUB_TOKEN=abc git push", "GITHUB_TOKEN=*** git push"},
		{`API_KEY="a b c" make`, "API_KEY=*** make"},
		{"SECRET='x y' run", "SECRET=*** run"},
		{"DB_PASSWORD=hunter2 ./go", "DB_PASSWORD=*** ./go"},
		{"mysql --password=hunter2 db", "mysql --password=*** db"},
		{"A=1 B_PASSWD=2", "A=1 B_PASSWD=***"},
		{"export HOME=/home/me", "export HOME=/home/me"},
		{`curl -H 'Authorization: Bearer abc.def' https://x`, `curl -H 'Authorization: ***' https://x`},
		{`curl -H "authorization:Basic Zm9v" u`, `curl -H "authorization:***" u`},
		{`curl -H "X-Api-Key: k123" u`, `curl -H "X-Api-Key: ***" u`},
		{`curl -H "Accept: json" u`, `curl -H "Accept: json" u`},
	} {
		if got := MaskSecrets(c.in); got != c.want {
			t.Errorf("MaskSecrets(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
