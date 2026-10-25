#!/usr/bin/env bash
# Fragt mögliche Temperatur-Endpunkte am Gateway ab und zeigt, welche das Gerät unterstützt.
#
# Nutzung:
#   ./scripts/probe-temperatures.sh                  # liest BUDERUS_HOST/BUDERUS_TOKEN aus .env
#   BUDERUS_HOST=192.168.2.97 BUDERUS_TOKEN=... ./scripts/probe-temperatures.sh

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

paths=(
  # Übersichten (liefern auf manchen Gateways eine Liste der Unterpfade)
  /heatingCircuits
  /heatingCircuits/hc1
  /heatingCircuits/hc2
  /heatSources

  # Heizkreise (teils undokumentiert, von älteren Gateways bekannt)
  /heatingCircuits/hc1/actualSupplyTemperature
  /heatingCircuits/hc2/actualSupplyTemperature
  /heatingCircuits/hc1/maxFlowTemp
  /heatingCircuits/hc2/maxFlowTemp
  /heatingCircuits/hc1/mixerPosition
  /heatingCircuits/hc2/mixerPosition

  # System / Wärmeerzeuger
  /heatSources/actualSupplyTemperature
  /heatSources/currentSupplySetpoint
  /heatSources/returnTemperature
  /heatSources/hs1/returnFlowTemp
  /heatSources/hs1/supplyFlowCondenserTemp
  /system/sensors/temperatures/outdoor_t1
)

if command -v jq >/dev/null 2>&1; then
  format() { jq -c . 2>/dev/null || cat; }
else
  format() { cat; }
fi

for path in "${paths[@]}"; do
  url="https://${BUDERUS_HOST}:${BUDERUS_PORT}${path}"
  # -w hängt den HTTP-Status als letzte Zeile an.
  response=$(curl --silent --insecure --max-time 15 \
    --header "Authorization: Bearer ${BUDERUS_TOKEN}" \
    --write-out '\n%{http_code}' \
    "$url" || printf '\n000')

  status="${response##*$'\n'}"
  body="${response%$'\n'*}"

  printf '== %s [%s]\n' "$path" "$status"
  if [[ "$status" == "200" ]]; then
    printf '%s\n' "$body" | format
  fi
  echo
done
