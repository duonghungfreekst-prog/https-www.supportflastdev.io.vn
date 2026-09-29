#!/usr/bin/env bash
# ==============================================================================
# SUPPORTFLAST APP HUB - 1-CLICK DEPLOY SCRIPT (LINUX / MACOS)
# ==============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
UPLOADER="$SCRIPT_DIR/supportflast_uploader.py"

if ! command -v python3 &> /dev/null; then
    echo "[LOI] Python 3 chua duoc cai dat. Vui long cai dat python3."
    exit 1
fi

if [ ! -f "$UPLOADER" ]; then
    echo "[LOI] Khong tim thay supportflast_uploader.py tai $SCRIPT_DIR"
    exit 1
fi

chmod +x "$UPLOADER"
python3 "$UPLOADER" "$@"
