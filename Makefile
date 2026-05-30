.PHONY: build install uninstall clean setup

BINARY   := ghostline
INSTALL  := /usr/local/bin/$(BINARY)
DOT_DIR  := $(HOME)/.ghostline

build:
	go build -o $(BINARY) ./cmd/ghostline

install: build
	cp $(BINARY) $(INSTALL) 2>/dev/null || sudo cp $(BINARY) $(INSTALL)
	mkdir -p $(DOT_DIR)
	cp shell/ghostline.zsh $(DOT_DIR)/ghostline.zsh
	@grep -q 'ghostline.zsh' "$(HOME)/.zshrc" 2>/dev/null || \
		printf '\n# ghostline AI terminal assistant\nsource ~/.ghostline/ghostline.zsh\n' >> "$(HOME)/.zshrc"
	@echo ""
	@echo "ghostline installed."
	@echo "Run:  source ~/.zshrc"
	@echo ""
	@echo "First time? Also run:"
	@echo "  brew install ollama"
	@echo "  brew services start ollama"
	@echo "  ollama pull qwen2.5-coder:3b"
	@echo "  ghostline setup"

uninstall:
	-ghostline status &>/dev/null && pkill -f 'ghostline server' || true
	rm -f $(INSTALL)
	rm -rf $(DOT_DIR)
	@sed -i '' '/ghostline.zsh/d' "$(HOME)/.zshrc" 2>/dev/null || true
	@sed -i '' '/ghostline AI terminal/d' "$(HOME)/.zshrc" 2>/dev/null || true
	@echo "ghostline uninstalled."

setup:
	@./$(BINARY) setup

clean:
	rm -f $(BINARY)
