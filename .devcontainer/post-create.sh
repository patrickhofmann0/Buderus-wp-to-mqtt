#!/usr/bin/env bash
# Läuft einmalig nach dem Anlegen des Dev Containers (als vscode).
set -euo pipefail

# Named Volumes können root gehören, wenn sie vor dem Image angelegt wurden.
sudo chown -R vscode: /go/pkg/mod /home/vscode/.local/share/opencode

go mod download

echo "Go:            $(go version)"
echo "golangci-lint: $(golangci-lint version --short)"
echo "opencode:      $(opencode --version)"

# Die Anmeldung liegt im Volume opencode-data und übersteht Rebuilds.
if opencode auth list 2>/dev/null | grep -qv "No authenticated integrations"; then
  opencode auth list
else
  echo "HINWEIS: opencode ist noch nicht angemeldet. Einmalig im Container ausführen:" >&2
  echo "  opencode auth login   (GitHub Copilot auswählen)" >&2
fi
