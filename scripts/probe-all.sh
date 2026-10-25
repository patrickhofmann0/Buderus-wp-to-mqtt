#!/usr/bin/env bash
# Fragt alle in buderus.yml dokumentierten GET-Pfade am Gateway ab und zeigt,
# welche das Gerät unterstützt. Platzhalter wie {heatingCircuitId} werden durch
# die unten konfigurierbaren IDs ersetzt; Pfade mit nicht gesetzten IDs werden übersprungen.
#
# Nutzung:
#   ./scripts/probe-all.sh                      # liest BUDERUS_HOST/BUDERUS_TOKEN aus .env
#   ./scripts/probe-all.sh | tee probe-all.txt  # Ergebnis zusätzlich in Datei speichern
#   HEATING_CIRCUITS="hc1 hc2 hc3" ZONES="" ./scripts/probe-all.sh
#
# Variablen für Platzhalter (leer = Pfade überspringen):
#   HEATING_CIRCUITS  Default: "hc1 hc2"
#   DHW_CIRCUITS      Default: "dhw1"
#   HEAT_SOURCES      Default: "hs1"
#   ZONES             Default: "zone1"
#   DEVICES, VENTILATION_ZONES, SOLAR_CIRCUITS, SIGNALS  Default: leer
#   INCLUDE_RECORDINGS=1  auch /recordings/... abfragen (mit sampleRate=P1M), Default: aus

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
SPEC="${SPEC:-buderus.yml}"

HEATING_CIRCUITS="${HEATING_CIRCUITS-hc1 hc2}"
DHW_CIRCUITS="${DHW_CIRCUITS-dhw1}"
HEAT_SOURCES="${HEAT_SOURCES-hs1}"
ZONES="${ZONES-zone1}"
DEVICES="${DEVICES-}"
VENTILATION_ZONES="${VENTILATION_ZONES-}"
SOLAR_CIRCUITS="${SOLAR_CIRCUITS-}"
SIGNALS="${SIGNALS-}"
INCLUDE_RECORDINGS="${INCLUDE_RECORDINGS-0}"

# expand ersetzt einen Platzhalter durch alle angegebenen IDs.
# Liest Pfade von stdin, schreibt erweiterte Pfade nach stdout.
expand() {
  local placeholder="$1" ids="$2" path id
  while IFS= read -r path; do
    if [[ "$path" != *"{$placeholder}"* ]]; then
      printf '%s\n' "$path"
      continue
    fi
    for id in $ids; do
      printf '%s\n' "${path//\{$placeholder\}/$id}"
    done
  done
}

# Alle Pfade aus der Spec: Zeilen der Form "  /pfad:" unterhalb von "paths:".
mapfile -t paths < <(
  grep -E '^  /[^ ]*:$' "$SPEC" \
    | sed -E 's/^  (.*):$/\1/' \
    | expand heatingCircuitId "$HEATING_CIRCUITS" \
    | expand dhwCircuitId "$DHW_CIRCUITS" \
    | expand heatSourceId "$HEAT_SOURCES" \
    | expand zoneId "$ZONES" \
    | expand deviceId "$DEVICES" \
    | expand ventilationZoneId "$VENTILATION_ZONES" \
    | expand solarCircuitId "$SOLAR_CIRCUITS" \
    | expand signalId "$SIGNALS" \
    | grep -v '{' \
    | sort -u
)

if command -v jq >/dev/null 2>&1; then
  have_jq=1
else
  have_jq=0
  echo "Hinweis: jq ist nicht installiert, Antworten werden ungekürzt angezeigt." >&2
fi

# summarize kürzt eine Antwort auf das Wesentliche (Wert + Einheit).
summarize() {
  if [[ $have_jq -eq 1 ]]; then
    jq -c '
      if has("value") then
        {value, unit: (.unitOfMeasure // .unit)} | with_entries(select(.value != null))
      elif has("values") then
        {values, unit: (.unitOfMeasure // .unit)} | with_entries(select(.value != null))
      else . end
    ' 2>/dev/null | cut -c1-300
  else
    cut -c1-300
  fi
}

declare -A counts=()
supported=()

for path in "${paths[@]}"; do
  query=""
  if [[ "$path" == /recordings/* ]]; then
    [[ "$INCLUDE_RECORDINGS" == "1" ]] || continue
    query="?sampleRate=P1M"
  fi

  response=$(curl --silent --insecure --max-time 15 \
    --header "Authorization: Bearer ${BUDERUS_TOKEN}" \
    --write-out '\n%{http_code}' \
    "https://${BUDERUS_HOST}:${BUDERUS_PORT}${path}${query}" || printf '\n000')

  status="${response##*$'\n'}"
  body="${response%$'\n'*}"
  counts[$status]=$(( ${counts[$status]:-0} + 1 ))

  if [[ "$status" == "200" ]]; then
    summary=$(printf '%s\n' "$body" | summarize)
    printf '[200] %s  %s\n' "$path" "$summary"
    supported+=("$path")
  else
    printf '[%s] %s\n' "$status" "$path"
  fi
done

echo
echo "== Zusammenfassung"
for status in "${!counts[@]}"; do
  printf '  %s: %d\n' "$status" "${counts[$status]}"
done | sort

echo
echo "== Unterstützte Pfade (${#supported[@]})"
if [[ ${#supported[@]} -gt 0 ]]; then
  printf '  %s\n' "${supported[@]}"
fi
