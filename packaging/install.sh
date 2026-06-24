#!/bin/bash
set -e

# Get the directory of the script and project root
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Define target paths
MOZ_NMH_DIR="$HOME/.mozilla/native-messaging-hosts"
TARGET_JSON="$MOZ_NMH_DIR/exteditor.json"
TARGET_BIN_DIR="$HOME/.local/bin/exteditor-nmh"
TARGET_BIN="$TARGET_BIN_DIR/exteditor"

# Check if already installed
if [ -f "$TARGET_JSON" ] || [ -f "$TARGET_BIN" ]; then
    echo "Error: exteditor is already installed."
    echo "Please run uninstall.sh first, and then run install.sh again."
    exit 1
fi

# Check if the binary file exists
SRC_BIN="./exteditor"
if [ ! -f "$SRC_BIN" ]; then
    echo "Error: Native messaging host binary not found at $SRC_BIN"
    echo "Please build the project first (e.g., using 'make build' in the native directory)."
    exit 1
fi

# Create directories if they do not exist
mkdir -p "$MOZ_NMH_DIR"
mkdir -p "$TARGET_BIN_DIR"

cp "$SRC_BIN" "$TARGET_BIN"
chmod +x "$TARGET_BIN"

# Generate exteditor.json from template and place it
sed "s|@@NATIVE_PATH@@|$TARGET_BIN|" "./exteditor.json.in" > "$TARGET_JSON"

echo "Installation completed successfully."
echo "Config: $TARGET_JSON"
echo "Binary: $TARGET_BIN"
