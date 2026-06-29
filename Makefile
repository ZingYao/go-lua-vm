SHELL := /bin/sh

GO ?= go
NPM ?= npm
NPX ?= npx
GRADLE ?= gradle

BIN_DIR ?= bin
DIST_DIR ?= dist
VSCODE_EXTENSION_DIR := vscode/extensions/glua-lsp
JETBRAINS_EXTENSION_DIR := jetbrains/extensions/glua-lsp

CLI_CMDS := glua gluac gluals

TARGETS := \
	linux-amd64 \
	linux-386 \
	linux-arm64 \
	linux-armv6 \
	linux-armv7 \
	windows-amd64 \
	windows-386 \
	windows-arm64 \
	darwin-amd64 \
	darwin-arm64 \
	android-arm64

.PHONY: all build build-ls dist $(TARGETS) package-vscode package-jetbrains package-extensions clean

all: build

build:
	@mkdir -p "$(BIN_DIR)"
	@for cmd in $(CLI_CMDS); do \
		echo "building $$cmd"; \
		CGO_ENABLED=0 "$(GO)" build -trimpath -ldflags="-s -w" -o "$(BIN_DIR)/$$cmd" "./cmd/$$cmd"; \
	done

build-ls:
	@mkdir -p "$(BIN_DIR)"
	@echo "building gluals"
	@CGO_ENABLED=0 "$(GO)" build -trimpath -ldflags="-s -w" -o "$(BIN_DIR)/gluals" ./cmd/gluals

dist: $(TARGETS)

$(TARGETS):
	@set -eu; \
	target="$@"; \
	goos="$${target%%-*}"; \
	arch_part="$${target#*-}"; \
	goarch="$$arch_part"; \
	goarm=""; \
	case "$$arch_part" in \
		armv6) goarch="arm"; goarm="6" ;; \
		armv7) goarch="arm"; goarm="7" ;; \
	esac; \
	out_dir="$(DIST_DIR)/$$target"; \
	mkdir -p "$$out_dir"; \
	for cmd in $(CLI_CMDS); do \
		output="$$out_dir/$$cmd"; \
		if [ "$$goos" = "windows" ]; then output="$$output.exe"; fi; \
		echo "building $$cmd for $$target"; \
		if [ -n "$$goarm" ]; then \
			CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" GOARM="$$goarm" "$(GO)" build -trimpath -ldflags="-s -w" -o "$$output" "./cmd/$$cmd"; \
		else \
			CGO_ENABLED=0 GOOS="$$goos" GOARCH="$$goarch" "$(GO)" build -trimpath -ldflags="-s -w" -o "$$output" "./cmd/$$cmd"; \
		fi; \
	done

package-vscode:
	@mkdir -p "$(DIST_DIR)"
	@cd "$(VSCODE_EXTENSION_DIR)" && "$(NPM)" ci
	@cd "$(VSCODE_EXTENSION_DIR)" && "$(NPX)" @vscode/vsce package --out "../../../$(DIST_DIR)/glua-lsp-vscode.vsix"

package-jetbrains:
	@cd "$(JETBRAINS_EXTENSION_DIR)" && if [ -x ./gradlew ]; then ./gradlew --no-daemon buildPlugin; else "$(GRADLE)" --no-daemon buildPlugin; fi
	@mkdir -p "$(DIST_DIR)"
	@cp "$(JETBRAINS_EXTENSION_DIR)"/build/distributions/*.zip "$(DIST_DIR)/"

package-extensions: package-vscode package-jetbrains

clean:
	@rm -rf "$(BIN_DIR)" "$(DIST_DIR)"
	@rm -rf "$(VSCODE_EXTENSION_DIR)"/*.vsix
	@rm -rf "$(JETBRAINS_EXTENSION_DIR)"/build
