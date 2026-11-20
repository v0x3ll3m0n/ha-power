# Build, test and package this extension.
#
#   make core      fetch the pinned Prusa Connect checkout into .core/
#   make client    regenerate the typed client in client/ from openapi.json
#   make check     everything CI runs: Go vet and tests, the client is current, the UI's lint, types, tests and build
#   make release   the release directory the marketplace takes, in release/
#   make install   install release/ into a local Prusa Connect data dir, to try it
#
# Until the Prusa Connect SDK and the @prusa npm packages are published, both
# halves build against a checkout of Prusa Connect at CORE_REF. Raise CORE_REF
# to build against a newer core, and raise `require.core` in module.yaml with it.

CORE_REPO ?= https://github.com/prusa3d/prusa-connect.git
CORE_REF ?= v0.1.0-beta.1
CORE := .core

NAME := $(shell sed -n 's/^name:[[:space:]]*//p' module.yaml | head -1)
VERSION := $(shell sed -n 's/^version:[[:space:]]*//p' module.yaml | head -1)
MODULE := $(shell sed -n 's/^module[[:space:]]*//p' go.mod | head -1)
# github.com/acme/spools -> com.github.aux.acme.spools, the host's own rule for
# a GitHub module path.
NAMESPACE := $(shell echo $(MODULE) | sed -n 's|^github\.com/\([^/]*\)/\([^/]*\)$$|com.github.aux.\1.\2|p')
PLATFORMS := $(shell sh scripts/platforms.sh)

RELEASE := release
PLAYWRIGHT_FLAGS ?=
export CI ?= true

.PHONY: core client client-check check go-check ui-check ui-build release install clean

# The checkout is cloned once. To move to another CORE_REF, `rm -rf .core`
# first.
$(CORE)/.git:
	git -c advice.detachedHead=false clone --quiet --depth 1 --branch $(CORE_REF) $(CORE_REPO) $(CORE)

# The component library ships the Vite configuration the bundle builds with,
# through its `build` export, which points at the library's dist.
$(CORE)/packages/svelte/dist/build/module.js: | $(CORE)/.git
	cd $(CORE)/packages/svelte && pnpm install --frozen-lockfile
	cd $(CORE)/packages/svelte && pnpm run build

# The core's typed client, which the generated client in client/ builds against.
$(CORE)/packages/typescript/dist/index.js: | $(CORE)/.git
	cd $(CORE)/packages/typescript && pnpm install --frozen-lockfile
	cd $(CORE)/packages/typescript && pnpm run build

core: $(CORE)/packages/svelte/dist/build/module.js $(CORE)/packages/typescript/dist/index.js

check: go-check client-check ui-check

go-check: core
	go vet ./...
	go test ./...

# The typed client the page calls the power routes with. It is generated from
# openapi.json, the same fragment the process embeds, and committed.
client: core
	go -C $(CORE) run ./cmd/generate-client-sdk -module -document "$(CURDIR)/openapi.json" \
		-namespace $(NAMESPACE) -name ha-power-client -out "$(CURDIR)/client"
	cd client && pnpm install --frozen-lockfile && pnpm run generate:types

# Fails when openapi.json changed and nobody ran `make client`.
client-check: client/node_modules/.modules.yaml
	go -C $(CORE) run ./cmd/generate-client-sdk -module -check -document "$(CURDIR)/openapi.json" \
		-namespace $(NAMESPACE) -name ha-power-client -out "$(CURDIR)/client"
	cd client && pnpm run generate:types && git diff --exit-code src/generated/

client/node_modules/.modules.yaml: client/package.json client/pnpm-lock.yaml | core
	cd client && pnpm install --frozen-lockfile

client/dist/index.js: client/node_modules/.modules.yaml $(wildcard client/src/*.ts client/src/generated/*.ts)
	cd client && pnpm run build

ui/node_modules/.modules.yaml: ui/package.json ui/pnpm-lock.yaml client/dist/index.js | core
	cd ui && pnpm install --frozen-lockfile

# The component tests run in Chromium. CI sets PLAYWRIGHT_FLAGS=--with-deps.
ui-check: ui/node_modules/.modules.yaml
	cd ui && pnpm exec playwright install $(PLAYWRIGHT_FLAGS) chromium
	cd ui && pnpm run check
	cd ui && pnpm run build

ui-build: ui/node_modules/.modules.yaml
	cd ui && pnpm run build

# ui/dist is already the release layout: module.yaml at its root and the
# bundle under ui/. Each binary is built with CGO off and -trimpath, so it runs
# on any host of its platform and names no path of the machine that built it.
release: ui-build core
	rm -rf $(RELEASE)
	cp -R ui/dist $(RELEASE)
	cp LICENSE README.md CHANGELOG.md $(RELEASE)/
	@for platform in $(PLATFORMS); do \
		goos=$${platform%%_*}; goarch=$${platform#*_}; \
		binary=$(NAME)_$$platform; [ $$goos = windows ] && binary=$$binary.exe; \
		echo "building $$binary"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -trimpath -ldflags='-s -w' -o $(RELEASE)/$$binary . || exit 1; \
		chmod +x $(RELEASE)/$$binary; \
	done
	@echo "$(NAME) $(VERSION) is in $(RELEASE)/"

# Installs release/ into bin/try with the pinned core's own `module add`, then
# prints how to start that core with it. Only this machine's binary is needed.
TRY := $(CURDIR)/bin/try
install: release
	mkdir -p $(TRY)
	printf 'core:\n  overlay: %s/resources.json\n  modules: %s/modules\n  authz:\n    database: %s/authz.db\n  api:\n    addr: 127.0.0.1:18480\n' \
		"$(TRY)" "$(TRY)" "$(TRY)" > $(TRY)/config.yaml
	go -C $(CORE) run ./cmd/prusa-connect module add "$(MODULE)@$(VERSION)" \
		-config "$(TRY)/config.yaml" -from "$(CURDIR)/$(RELEASE)" -approve
	@echo
	@echo "Installed. Build the core with its web app once:  make -C $(CORE) build"
	@echo "Then start it:                                    (cd $(TRY) && $(CURDIR)/$(CORE)/bin/server -config config.yaml)"
	@echo "Finish the first run at http://127.0.0.1:18480 with the setup code it logs, and open Hello in the sidebar."

clean:
	rm -rf $(RELEASE) bin ui/dist ui/node_modules client/dist client/node_modules
