#!/bin/sh
# Build donecheck from this checkout and install it as a command (Linux/macOS).
# Usage: ./install.sh            installs to ~/.local/bin
#        DONECHECK_INSTALL_DIR=/usr/local/bin ./install.sh
set -eu

src_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bin_dir=${DONECHECK_INSTALL_DIR:-$HOME/.local/bin}

if ! command -v go >/dev/null 2>&1; then
	echo "install.sh: Go is not installed or not on PATH. Install Go 1.25+ from https://go.dev/dl/ and retry." >&2
	exit 1
fi

mkdir -p "$bin_dir"
echo "Building donecheck with $(go version) ..."
(cd "$src_dir" && go build -trimpath -o "$bin_dir/donecheck" .)

# Prove the installed binary actually runs before claiming success.
"$bin_dir/donecheck" help >/dev/null
echo "Installed: $bin_dir/donecheck"

case ":$PATH:" in
*":$bin_dir:"*) echo "Ready. Try: donecheck help" ;;
*)
	echo
	echo "$bin_dir is not on your PATH. Add this line to your shell profile (~/.bashrc, ~/.zshrc, ...):"
	echo "  export PATH=\"$bin_dir:\$PATH\""
	echo "Then open a new terminal and run: donecheck help"
	;;
esac
