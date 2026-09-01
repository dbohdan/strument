.PHONY: build check clean

WASM_TARGET = wasm32-wasip1
# The blob is embedded into the Go package one level up
# (internal/monty/monty.wasm) and consumed by `go build` from there.
WASM_BINARY = ../monty.wasm

# Rust's equivalent of Go's -trimpath: rewrite the absolute paths rustc would
# otherwise embed (panic locations, debuginfo) so the blob carries no local
# paths and builds are reproducible across machines. Git dependencies like
# `monty` are checked out under the cargo home, so that must be remapped too.
# CARGO_HOME is often unset in the environment; rustc then defaults to
# $(HOME)/.cargo, so resolve it explicitly.
CARGO_HOME ?= $(HOME)/.cargo
RUSTFLAGS = --remap-path-prefix=$(CURDIR)=/build \
            --remap-path-prefix=$(CARGO_HOME)=/cargo

# Drop DWARF from the release blob: smaller, and the panic-location strings
# are all that remains for the remap above to cover.
CARGO_PROFILE_RELEASE_STRIP = debuginfo

# Build the WASM binary from the Rust shim and install it into the Go
# package. Requires the wasm32-wasip1 Rust target.
build:
	RUSTFLAGS="$(RUSTFLAGS)" CARGO_PROFILE_RELEASE_STRIP=$(CARGO_PROFILE_RELEASE_STRIP) \
	cargo build --target $(WASM_TARGET) -p monty-wasm --release
	cp target/$(WASM_TARGET)/release/monty_wasm.wasm $(WASM_BINARY)

# Build and verify: the blob must carry no local paths, and the Go suite that
# exercises it must pass.
check: build
	strings -n 8 $(WASM_BINARY) | grep -cE '/home|/tmp/|/root' | grep -qx 0 || \
		{ echo "the blob embeds local paths"; exit 1; }
	cd ../../.. && go test ./internal/monty/... ./internal/coder/...

clean:
	cargo clean
