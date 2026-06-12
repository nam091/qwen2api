#!/usr/bin/env bash
set -euo pipefail

echo "============================================"
echo "  qwen2api Build Script (Linux/macOS)"
echo "============================================"
echo

# Check if Go is installed
if ! command -v go &>/dev/null; then
    echo "[ERROR] Go is not installed."
    echo "Install: https://go.dev/dl/"
    echo "  Linux:  sudo apt install golang-go  OR  sudo snap install go"
    echo "  macOS:  brew install go"
    exit 1
fi

echo "[OK] Go found: $(go version)"

# Navigate to project root
cd "$(dirname "$0")"

# Download dependencies
echo
echo "[1/3] Downloading dependencies..."
go mod tidy
echo "[OK] Dependencies downloaded"

# Detect OS/Arch
GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)
OUTPUT="qwen2api"
if [ "$GOOS" = "windows" ]; then
    OUTPUT="qwen2api.exe"
fi

# Build
echo
echo "[2/3] Building ${OUTPUT} for ${GOOS}/${GOARCH}..."
CGO_ENABLED=1 go build -ldflags="-s -w" -o "$OUTPUT" ./cmd/qwen2api/
echo "[OK] Build successful: ${OUTPUT}"

# Make executable
chmod +x "$OUTPUT"

echo
echo "[3/3] Done!"
echo
echo "To run:  ./${OUTPUT}"
echo "To test: curl http://localhost:5001/healthz"
echo

read -rp "Run now? (y/N): " RUN_NOW
if [[ "${RUN_NOW,,}" == "y" ]]; then
    echo "Starting qwen2api..."
    ./"$OUTPUT"
fi
