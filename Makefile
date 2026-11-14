.PHONY: build test integration cover fuzz vet fmt check clean dist deb clean-dist

build:
	go build -o bin/safeshell ./cmd/safeshell

test:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1

# Short fuzz runs of the parser and classifier; raise FUZZTIME for longer.
FUZZTIME ?= 20s
fuzz:
	go test ./internal/shellparse -run xxx -fuzz FuzzParse -fuzztime $(FUZZTIME)
	go test ./internal/classify -run xxx -fuzz FuzzClassify -fuzztime $(FUZZTIME)

# Runs the real binary against the real kernel; needs the AppArmor profile (scripts/install-apparmor.sh).
integration: build
	go test -count=1 -v ./test/integration

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# Everything that must pass before a commit.
check: vet
	bash test/scripts/install_test.sh
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go test -race -covermode=atomic -coverprofile=coverage.out ./...
	GOOS=linux GOARCH=arm64 go vet ./...
	GOOS=linux GOARCH=riscv64 go vet ./...

clean:
	rm -rf bin coverage.out

# Release artefacts. CGO is off, so the binaries are static and run on any Linux.
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
dist: clean-dist
	@for arch in amd64 arm64; do \
	  d=dist/safeshell-$(VERSION)-linux-$$arch; mkdir -p $$d; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $$d/safeshell ./cmd/safeshell || exit 1; \
	  cp -r scripts packaging $$d/; \
	  tar -czf dist/safeshell-$(VERSION)-linux-$$arch.tar.gz -C dist safeshell-$(VERSION)-linux-$$arch; \
	done
	@cd dist && sha256sum *.tar.gz > SHA256SUMS && ls -l *.tar.gz

# Debian versions must start with a digit: v1.2.3 -> 1.2.3, anything else -> 0~that.
DEBVERSION := $(shell echo '$(VERSION)' | sed -e 's/^v//' -e 's/^[^0-9]/0~&/')

# The maintainer is the one in debian/control.
DEBMAINTAINER := $(shell sed -n 's/^Maintainer: //p' debian/control)

# Debian packages for amd64 and arm64: install /usr/bin/safeshell and the AppArmor rule for exactly that path.
deb:
	@rm -rf dist/deb; mkdir -p dist
	@command -v dpkg-deb >/dev/null || { echo "dpkg-deb not found"; exit 1; }
	@for arch in amd64 arm64; do \
	  root=dist/deb/$$arch; mkdir -p $$root/DEBIAN $$root/usr/bin $$root/etc/apparmor.d; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $$root/usr/bin/safeshell ./cmd/safeshell || exit 1; \
	  sed 's#@BIN@#/usr/bin/safeshell#' packaging/apparmor/safeshell-ss.tmpl > $$root/etc/apparmor.d/safeshell-ss; \
	  mkdir -p $$root/usr/share/man/man1 $$root/usr/share/doc/safeshell; \
	  gzip -9n -c debian/safeshell.1 > $$root/usr/share/man/man1/safeshell.1.gz; \
	  cp debian/copyright $$root/usr/share/doc/safeshell/copyright; \
	  gzip -9n -c debian/changelog > $$root/usr/share/doc/safeshell/changelog.Debian.gz; \
	  mkdir -p $$root/usr/share/lintian/overrides; cp packaging/deb/lintian-overrides $$root/usr/share/lintian/overrides/safeshell; \
	  cp packaging/deb/postinst packaging/deb/postrm $$root/DEBIAN/; \
	  echo /etc/apparmor.d/safeshell-ss > $$root/DEBIAN/conffiles; \
	  printf 'Package: safeshell\nVersion: $(DEBVERSION)\nSection: utils\nPriority: optional\nArchitecture: %s\nMaintainer: $(DEBMAINTAINER)\nRecommends: apparmor\nHomepage: https://github.com/samarthsinh2660/safeshell\nDescription: safety layer for your terminal\n Rates every command by how reversible it is, shows what it would do\n in a sandbox, blocks the dangerous ones, and lets you undo.\n Optional built-in AI.\n' $$arch > $$root/DEBIAN/control; \
	  find $$root -type d -exec chmod 755 {} +; chmod 755 $$root/usr/bin/safeshell; chmod 644 $$root/etc/apparmor.d/safeshell-ss $$root/usr/share/lintian/overrides/safeshell $$root/usr/share/man/man1/safeshell.1.gz $$root/usr/share/doc/safeshell/*; \
	  dpkg-deb --build --root-owner-group $$root dist/safeshell_$(DEBVERSION)_$$arch.deb || exit 1; \
	done
	@ls -l dist/*.deb

clean-dist:
	@find dist -mindepth 1 -delete 2>/dev/null; mkdir -p dist
