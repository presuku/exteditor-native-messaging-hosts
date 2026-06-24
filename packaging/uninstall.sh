#!/bin/bash
set -e

MOZ_NMH_DIR="$HOME/.mozilla/native-messaging-hosts"
TARGET_JSON="$MOZ_NMH_DIR/exteditor.json"
TARGET_BIN_DIR="$HOME/.local/bin/exteditor-nmh"

echo "Uninstalling exteditor..."

if [ -f "$TARGET_JSON" ]; then
    rm -f "$TARGET_JSON"
    echo "Removed $TARGET_JSON"
fi

if [ -d "$TARGET_BIN_DIR" ]; then
    rm -rf "$TARGET_BIN_DIR"
    echo "Removed $TARGET_BIN_DIR directory"
fi

echo "Uninstallation completed successfully."
