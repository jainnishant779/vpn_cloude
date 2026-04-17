#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

VERSION="${VERSION:-$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || echo "dev")}" 
TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

echo "Building quicktunnel client version ${VERSION}"

for target in "${TARGETS[@]}"; do
  GOOS="${target%%/*}"
  GOARCH="${target##*/}"
  BIN_NAME="quicktunnel"

  if [[ "${GOOS}" == "windows" ]]; then
    BIN_NAME="quicktunnel.exe"
  fi

  OUTPUT="${DIST_DIR}/quicktunnel-${GOOS}-${GOARCH}"
  if [[ "${GOOS}" == "windows" ]]; then
    OUTPUT+=".exe"
  fi

  echo " -> ${GOOS}/${GOARCH}"
  (
    cd "${ROOT_DIR}"
    GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 \
      go build -trimpath -ldflags "-X main.version=${VERSION} -s -w" -o "${OUTPUT}" ./client/cmd/quicktunnel
  )
done

echo "Build output available in ${DIST_DIR}"
