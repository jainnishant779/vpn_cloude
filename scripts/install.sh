#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
esac

case "${OS}" in
  linux*) OS="linux" ;;
  darwin*) OS="darwin" ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *) echo "Unsupported OS: ${OS}"; exit 1 ;;
esac

BINARY="${DIST_DIR}/quicktunnel-${OS}-${ARCH}"
if [[ "${OS}" == "windows" ]]; then
  BINARY+=".exe"
fi

if [[ ! -f "${BINARY}" ]]; then
  echo "Binary not found at ${BINARY}. Run scripts/build.sh first."
  exit 1
fi

if [[ "${OS}" == "windows" ]]; then
  INSTALL_DIR="${APPDATA:-$HOME/AppData/Roaming}/QuickTunnel"
  mkdir -p "${INSTALL_DIR}"
  cp "${BINARY}" "${INSTALL_DIR}/quicktunnel.exe"
  chmod +x "${INSTALL_DIR}/quicktunnel.exe"
  echo "Installed quicktunnel to ${INSTALL_DIR}/quicktunnel.exe"
  echo "Add ${INSTALL_DIR} to your PATH if needed."
else
  INSTALL_DIR="/usr/local/bin"
  if [[ ! -w "${INSTALL_DIR}" ]]; then
    echo "Installing with sudo to ${INSTALL_DIR}"
    sudo cp "${BINARY}" "${INSTALL_DIR}/quicktunnel"
    sudo chmod +x "${INSTALL_DIR}/quicktunnel"
  else
    cp "${BINARY}" "${INSTALL_DIR}/quicktunnel"
    chmod +x "${INSTALL_DIR}/quicktunnel"
  fi
  echo "Installed quicktunnel to ${INSTALL_DIR}/quicktunnel"
fi

mkdir -p "${HOME}/.quicktunnel"

cat <<EOF
Setup complete.

Next steps:
1. Run quicktunnel login --email <email> --password <password>
2. Run quicktunnel config --set network_id=<network-id>
3. Run quicktunnel up
EOF
