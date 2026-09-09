# Thin Makefile wrapper for lgo_download_manager (ldm).
#
# All real build work on Windows hosts is done by scripts/build.ps1.
# This Makefile just exists so users on Windows can keep typing
# `make <target>` (and so CI / shells that only know GNU Make can keep
# driving things). On non-Windows hosts the simple targets (build,
# vet, clean, version) fall through to a plain `go` invocation;
#
# Quick reference:
#   make help              - print this summary
#   make build             - build current platform binary into bin/
#   make build-windows     - cross-build ldm.exe (windows/amd64)
#   make icon              - regenerate assets/ldm.ico via scripts/gen_icon.py
#   make installer         - build Windows installer (auto-fetches ISCC)
#   make iscc-fetch        - download the Inno Setup 6.7.3 bootstrap only
#   make iscc              - extract ISCC.exe from the cached bootstrap
#   make vet               - run go vet ./...
#   make version           - print resolved build version
#   make clean             - remove bin/, dist/, .tools/

GO       ?= go
PYTHON   ?= python
PWSH     ?= pwsh
PS_SCRIPT := scripts/build.ps1

# Windows detection: this host is Windows if the OS env var matches
# Windows_NT (set by both cmd.exe and PowerShell) or if we're running
# under MSYS/Cygwin (whose uname -s also says MSYS_NT-10.0-…).
ifeq ($(OS),Windows_NT)
    ON_WINDOWS := 1
endif
ifndef ON_WINDOWS
    UNAME_S := $(shell uname -s 2>/dev/null)
    ifneq (,$(findstring MSYS,$(UNAME_S)))
        ON_WINDOWS := 1
    endif
    ifneq (,$(findstring CYGWIN,$(UNAME_S)))
        ON_WINDOWS := 1
    endif
    ifneq (,$(findstring MINGW,$(UNAME_S)))
        ON_WINDOWS := 1
    endif
endif

.DEFAULT_GOAL := help

.PHONY: help
help:                           ## Show available targets.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ----------------------------------------------------------------------------
# Forwarding layer
#
# Targets that exist on both Windows (via PS1) and non-Windows (via go)
# are routed by ON_WINDOWS. Windows-only targets fall through to PS1
# unconditionally.
# ----------------------------------------------------------------------------

.PHONY: version
version:                        ## Print resolved build version.
ifeq ($(ON_WINDOWS),1)
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) version
else
	@echo "VERSION=$$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)"
	@echo "COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
	@echo "BUILD_DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	@echo "GOOS=$$($(GO) env GOOS) GOARCH=$$($(GO) env GOARCH)"
endif

.PHONY: build
build:                          ## Build current platform binary into bin/.
ifeq ($(ON_WINDOWS),1)
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) build
else
	@$(GO) build -trimpath -o bin/ldm .
endif

.PHONY: build-windows
build-windows:                 ## Cross-build ldm.exe (windows/amd64).
ifeq ($(ON_WINDOWS),1)
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) build-windows
else
	@GOOS=windows GOARCH=amd64 $(GO) build -trimpath -o bin/ldm.exe .
endif

.PHONY: icon
icon:                           ## Regenerate assets/ldm.ico via Pillow.
ifeq ($(ON_WINDOWS),1)
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) icon
else
	@$(PYTHON) scripts/gen_icon.py
endif

.PHONY: vet
vet:                            ## Run go vet ./...
	@$(GO) vet ./...

.PHONY: clean
clean:                          ## Remove bin/, dist/, and .tools/.
ifeq ($(ON_WINDOWS),1)
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) clean
else
	-@rm -rf bin dist .tools
endif

# ----------------------------------------------------------------------------
# Windows-only: defer entirely to PS1.
# ----------------------------------------------------------------------------

.PHONY: installer
installer:                      ## Build Windows installer (requires Windows host).
ifndef ON_WINDOWS
	@echo "make installer requires Windows. Run directly instead:"
	@echo "  pwsh -File $(PS_SCRIPT) installer"
	@exit 1
endif
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) installer

.PHONY: iscc-fetch
iscc-fetch:                     ## Download the Inno Setup 6.7.3 bootstrap (Windows).
ifndef ON_WINDOWS
	@echo "make iscc-fetch requires Windows. Run directly instead:"
	@echo "  pwsh -File $(PS_SCRIPT) iscc-fetch"
	@exit 1
endif
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) iscc-fetch

.PHONY: iscc
iscc:                           ## Extract ISCC.exe from the cached bootstrap (Windows).
ifndef ON_WINDOWS
	@echo "make iscc requires Windows. Run directly instead:"
	@echo "  pwsh -File $(PS_SCRIPT) iscc"
	@exit 1
endif
	@$(PWSH) -NoProfile -File $(PS_SCRIPT) iscc