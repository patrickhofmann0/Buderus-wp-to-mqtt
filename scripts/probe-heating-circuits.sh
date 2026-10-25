#!/usr/bin/env bash
# Fragt Sollwerte und Status pro Heizkreis ab, inkl. undokumentierter Pfade älterer Gateways.
#
# Nutzung:
#   ./scripts/probe-heating-circuits.sh              # Heizkreise hc1 und hc2, liest .env
#   ./scripts/probe-heating-circuits.sh hc1 hc2 hc3  # eigene Heizkreise
#   BUDERUS_HOST=192.168.2.97 BUDERUS_TOKEN=... ./scripts/probe-heating-circuits.sh

set -euo pipefail

cd "$(dirname "$0")/.."

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

: "${BUDERUS_HOST:?BUDERUS_HOST is not set}"
: "${BUDERUS_TOKEN:?BUDERUS_TOKEN is not set}"
BUDERUS_PORT="${BUDERUS_PORT:-9443}"

circuits=("$@")
if [[ ${#circuits[@]} -eq 0 ]]; then
  circuits=(hc1 hc2)
fi

resources=(
  # undokumentiert (von älteren Gateways wie KM200 bekannt)
  supplyTemperatureSetpoint
  currentFlowSetpoint
  flowTemperatureSetpoint
  actualSupplyTemperature

  # dokumentiert
  currentRoomSetpoint
  roomtemperature
  maxFlowTemp
  mixerPosition
  currentSuWiMode
  overallStatus
  pumpStatus
)

if command -v jq >/dev/null 2>&1; then
  format() { jq -c . 2>/dev/null || cat; }
else
  format() { cat; }
fi

for hc in "${circuits[@]}"; do
  for resource in "${resources[@]}"; do
    path="/heatingCircuits/${hc}/${resource}"
    # -w hängt den HTTP-Status als letzte Zeile an.
    response=$(curl --silent --insecure --max-time 15 \
      --header "Authorization: Bearer ${BUDERUS_TOKEN}" \
      --write-out '\n%{http_code}' \
      "https://${BUDERUS_HOST}:${BUDERUS_PORT}${path}" || printf '\n000')

    status="${response##*$'\n'}"
    body="${response%$'\n'*}"

    printf '== %s [%s]\n' "$path" "$status"
    if [[ "$status" == "200" ]]; then
      printf '%s\n' "$body" | format
    fi
    echo
  done
done
