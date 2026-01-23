#!/usr/bin/env bash
set -euo pipefail

# Usage:
#   scripts/build.sh                # default: windows amd64
#   scripts/build.sh windows amd64  # explicit
#   scripts/build.sh linux amd64
#   scripts/build.sh darwin arm64

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

OS="${1:-windows}"
ARCH="${2:-amd64}"

BIN_NAME="digiflazz-api"
OUT_DIR="$ROOT_DIR/build"
mkdir -p "$OUT_DIR"

# Ensure modules are tidy
go mod tidy

# Build (SQLite is embedded via modernc.org/sqlite, so CGO disabled)
export CGO_ENABLED=0
export GOOS="$OS"
export GOARCH="$ARCH"

OUTFILE="$OUT_DIR/${BIN_NAME}-${OS}-${ARCH}"
if [[ "$OS" == "windows" ]]; then
  OUTFILE+=".exe"
fi

echo "Building $OUTFILE ..."
go build -ldflags "-s -w" -o "$OUTFILE" ./cmd/server
echo "OK: $OUTFILE"

# Function to create archive with binary and .env.example
create_archive() {
  local archive_name="${BIN_NAME}-${OS}-${ARCH}"
  local env_example="$ROOT_DIR/.env.example"
  local env_in_build="$OUT_DIR/.env.example"
  
  # Check if .env.example exists in build folder first, then root
  if [[ -f "$env_in_build" ]]; then
    env_example="$env_in_build"
    echo "Using .env.example from build folder"
  elif [[ -f "$env_example" ]]; then
    echo "Using .env.example from root folder"
  else
    echo "Warning: .env.example not found, archive will contain binary only"
    env_example=""
  fi
  
  # Determine archive format based on OS
  if [[ "$OS" == "windows" ]]; then
    archive_name+=".zip"
    ARCHIVE="$OUT_DIR/$archive_name"
    
    # Remove existing archive if any
    rm -f "$ARCHIVE"
    
    # Create zip archive
    cd "$OUT_DIR"
    if [[ -n "$env_example" ]]; then
      # Copy .env.example to build folder temporarily if it's not already there
      if [[ "$env_example" != "$env_in_build" ]]; then
        cp "$env_example" "$env_in_build" 2>/dev/null || true
      fi
      zip -q "$archive_name" "$(basename "$OUTFILE")" ".env.example" 2>/dev/null
      echo "Created archive: $ARCHIVE (contains binary and .env.example)"
      # Clean up copied file if it was copied
      if [[ "$env_example" != "$env_in_build" && -f "$env_in_build" ]]; then
        rm -f "$env_in_build"
      fi
    else
      zip -q "$archive_name" "$(basename "$OUTFILE")" 2>/dev/null
      echo "Created archive: $ARCHIVE (contains binary only)"
    fi
    cd "$ROOT_DIR"
  else
    archive_name+=".tar.gz"
    ARCHIVE="$OUT_DIR/$archive_name"
    
    # Remove existing archive if any
    rm -f "$ARCHIVE"
    
    # Create tar.gz archive
    cd "$OUT_DIR"
    if [[ -n "$env_example" ]]; then
      # Copy .env.example to build folder temporarily if it's not already there
      if [[ "$env_example" != "$env_in_build" ]]; then
        cp "$env_example" "$env_in_build" 2>/dev/null || true
      fi
      tar -czf "$archive_name" "$(basename "$OUTFILE")" ".env.example" 2>/dev/null
      echo "Created archive: $ARCHIVE (contains binary and .env.example)"
      # Clean up copied file if it was copied
      if [[ "$env_example" != "$env_in_build" && -f "$env_in_build" ]]; then
        rm -f "$env_in_build"
      fi
    else
      tar -czf "$archive_name" "$(basename "$OUTFILE")" 2>/dev/null
      echo "Created archive: $ARCHIVE (contains binary only)"
    fi
    cd "$ROOT_DIR"
  fi
  
  if [[ -f "$ARCHIVE" ]]; then
    local size=$(du -h "$ARCHIVE" | cut -f1)
    echo "Archive size: $size"
  else
    echo "Warning: Failed to create archive"
  fi
}

# Create archive
create_archive

