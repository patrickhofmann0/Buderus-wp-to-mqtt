#!/usr/bin/env bash
# Läuft vor jedem Start des Dev Containers auf dem Host (initializeCommand).
set -euo pipefail

cd "$(dirname "$0")"

if [ ! -f "$HOME/.config/opencode/cli.json" ]; then
  echo "Fehlt auf dem Host: ~/.config/opencode/cli.json (einmal opencode starten)" >&2
  exit 1
fi

# Dank Build-Cache nach dem ersten Mal in wenigen Sekunden erledigt.
# Kompletter Neubau: docker build --no-cache -t buderus2mqtt-devcontainer .devcontainer
docker build -t buderus2mqtt-devcontainer .
