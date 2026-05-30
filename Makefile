.PHONY: build build-linux-amd64 build-linux-arm64 build-darwin-arm64 build-all build-proxy install uninstall clean setup

BINARY   := ghostline
INSTALL  := /usr/local/bin/$(BINARY)
DOT_DIR  := $(HOME)/.ghostline

# ── Local build (current OS/arch) ─────────────────────────────────────────────
build:
	go build -o $(BINARY) ./cmd/ghostline

# ── Cross-compiled Linux builds ───────────────────────────────────────────────
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 ./cmd/ghostline

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o $(BINARY)-linux-arm64 ./cmd/ghostline

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -o $(BINARY)-darwin-arm64 ./cmd/ghostline

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY)-darwin-amd64 ./cmd/ghostline

# ── Managed proxy (deploy this on your server with your API key) ──────────────
build-proxy:
	GOOS=linux GOARCH=amd64 go build -o ghostline-proxy ./cmd/proxy

# ── Build all release variants ────────────────────────────────────────────────
build-all: build-linux-amd64 build-linux-arm64 build-darwin-arm64 build-darwin-amd64

# ── Local install (macOS dev workflow) ────────────────────────────────────────
install: build
	# Use install(1): writes to a temp file then renames atomically, always
	# allocating fresh disk blocks — prevents the macOS UE-sleep bug.
	install -m 755 $(BINARY) $(INSTALL) 2>/dev/null || sudo install -m 755 $(BINARY) $(INSTALL)
	mkdir -p $(DOT_DIR)
	cp shell/ghostline.zsh $(DOT_DIR)/ghostline.zsh
	cp shell/ghostline.bash $(DOT_DIR)/ghostline.bash
	chmod +x scripts/install.sh
	@grep -q 'ghostline.zsh' "$(HOME)/.zshrc" 2>/dev/null || \
		printf '\n# ghostline AI terminal assistant\nsource ~/.ghostline/ghostline.zsh\n' >> "$(HOME)/.zshrc"
	@echo ""
	@echo "ghostline installed."
	@echo "Run:  source ~/.zshrc"
	@echo ""
	@echo "First time? Set your API key, then verify:"
	@echo "  export ANTHROPIC_API_KEY=<your-key>   # (or OPENAI_API_KEY / GROQ_API_KEY)"
	@echo "  ghostline setup"

uninstall:
	-ghostline status &>/dev/null && pkill -f 'ghostline server' || true
	rm -f $(INSTALL)
	rm -rf $(DOT_DIR)
	@sed -i '' '/ghostline/d' "$(HOME)/.zshrc" 2>/dev/null || true
	@echo "ghostline uninstalled."

setup:
	@./$(BINARY) setup

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64 \
	      $(BINARY)-darwin-arm64 $(BINARY)-darwin-amd64 ghostline-proxy
