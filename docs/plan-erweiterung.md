# Plan: Erweiterung um weitere Werte (Leistung, Temperaturen, Status, Heizkreise, Wartung)

Stand: 2026-10-25. Dieser Plan beschreibt die nächsten Ausbauschritte. Der aktuelle Stand (Energiezähler Heizung/Warmwasser) ist davon unberührt und produktiv nutzbar.

Anlage, an der getestet wurde: Buderus **Logatherm WLW186i-12 E** (Luft/Wasser), Gateway MX400 (meldet sich als `K40RF_v1`, Firmware `15.00.01`), Systembus `EMS2_0`, 2 Heizkreise (HK1 ungemischt, HK2 gemischt), 1 Warmwasserkreis.

---

## Übersicht

| Schritt | Inhalt | Ergebnis |
|---|---|---|
| 1 | Skript `scripts/probe-signals.sh` | Liste aller Signale unter `/signals` mit Werten |
| 2 | Signale auswerten | Entscheidung, welche Signale zusätzlich übernommen werden |
| 3 | Gemeinsames Werte-Modell | `buderus.Value` für float/integer/string/Listen |
| 4 | Werte-Tabelle | `app/definitions.go` – alle Werte deklarativ |
| 5 | Gruppen-Poller | eine Goroutine pro Gruppe, eigenes Intervall |
| 6 | Berechnete Werte | Wärmeleistung und Live-COP |
| 7 | Home-Assistant-Discovery erweitern | `binary_sensor`, `enum`, weitere Device-Classes |
| 8 | Konfiguration | Gruppen an/aus, Intervalle, Kreis-IDs |
| 9 | Tests | echte Antworten als Testfälle |
| 10 | Doku | README, `.env.example` |

Getroffene Entscheidungen:

- Gruppen wie unten beschrieben, Defaults wie in der Tabelle.
- Wärmeleistung und Live-COP werden vom Programm berechnet.
- Intervall **pro Gruppe** konfigurierbar.
- `/signals` wird vorab mit einem Skript untersucht.

---

## Schritt 1: Skript `scripts/probe-signals.sh`

Ziel: herausfinden, ob die undokumentierten Signale Werte liefern, die über die REST-Pfade fehlen (insbesondere **Vorlauf-Ist/-Soll pro Heizkreis**).

Vorgehen (analog zu `scripts/probe-all.sh`):

1. `.env` laden, `BUDERUS_HOST`, `BUDERUS_TOKEN`, `BUDERUS_PORT` (Default 9443) prüfen.
2. `GET /signals` abrufen. Antwortformat:
   ```json
   {"id":"/signals","type":"refEnum","references":[
     {"id":"/signals/SRC.IntDHW.ExtraActive","uri":"https://192.168.2.97:9443/signals/SRC.IntDHW.ExtraActive"},
     {"id":"/signals/SRC.CUHP.HP1.ElectricalHeaterStep2","uri":"..."}, ...]}
   ```
3. Mit `jq -r '.references[].id'` alle IDs extrahieren (jq ist hier Pflicht, mit klarer Fehlermeldung, falls nicht installiert).
4. Optionaler Filter als erstes Argument (`grep -i`), z. B. `./scripts/probe-signals.sh HC`.
5. Jeden Pfad abfragen, Ausgabe pro Zeile: `[status] /signals/<name>  {"value":...,"unit":...}` (gleiche `summarize`-Funktion wie in `probe-all.sh`).
6. Zusammenfassung: Anzahl je Status, Liste aller Signale mit `200`.
7. `chmod +x`, mit `bash -n` und ggf. `shellcheck` prüfen.

Aufruf später: `./scripts/probe-signals.sh | tee probe-signals.txt`

## Schritt 2: Signale auswerten

- Ausgabe aus Schritt 1 sichten, Kandidaten suchen (Stichworte: `HC1`, `HC2`, `Flow`, `Supply`, `Setpoint`, `Mixer`, `Room`).
- Gefundene Signale in die Tabelle aus Schritt 4 aufnehmen (Pfad `/signals/<name>`).
- Ergebnis hier im Plan unter „Anhang C“ ergänzen.

---

## Schritt 3: Gemeinsames Werte-Modell (`internal/buderus/value.go`)

Die Endpunkte liefern unterschiedliche Typen:

| `type` | Beispiel | Go-Darstellung |
|---|---|---|
| `floatValue` | `{"value":30.6,"unitOfMeasure":"C","state":[{"short":32767.0},{"open":-32768.0}]}` | `float64` |
| `integerValue` | `{"value":49,"unitOfMeasure":"%"}` | `float64` (einheitlich) |
| `stringValue` | `{"value":"heating","allowedValues":[...]}` | `string` |
| `arrayData` / Listen | `{"values":["ch"]}` (actualHeatDemand) | `[]string` |
| Liste von Objekten | `{"values":[{"ch":737},{"dhw":194},{"total":931}]}` (numberOfStarts, workingTime) | `map[string]float64` |
| `emonValue` | bestehendes `EmonResponse` | `map[string]float64` |

Umsetzung:

1. Struct `Value` mit Feldern `ID`, `Type`, `Unit` (aus `unitOfMeasure` **oder** `unit`), `Raw json.RawMessage` für `value`/`values`, `State []map[string]float64`.
2. Methoden:
   - `Float() (float64, bool)` – liefert `false` bei ungültigem Fühlerwert.
   - `String() (string, bool)`
   - `Strings() ([]string, bool)`
   - `Map() (map[string]float64, bool)` – für Listen von Einzelobjekten (wie bisher `EmonValues.UnmarshalJSON`).
3. **Filter für ungültige Fühlerwerte**: Ein Wert ist ungültig, wenn er einem Wert aus `state` entspricht (`32767`, `-32768`) oder dem skalierten Platzhalter (`3276.7`, `-3276.8`). Vergleich mit kleiner Toleranz.
4. Client: neue Methode `FetchValue(ctx, path) (Value, error)`. `FetchEmon` bleibt vorerst bestehen oder wird auf `FetchValue` + `Map()` umgestellt.
5. Fehler für `404` als eigener Fehlertyp (`ErrNotFound`, prüfbar mit `errors.Is`), damit der Poller den Pfad überspringen kann.

## Schritt 4: Werte-Tabelle (`internal/app/definitions.go`)

Jeder Wert wird genau einmal beschrieben; daraus entstehen Abfrage, MQTT-JSON und Discovery.

```go
type Definition struct {
    Group     string        // "power", "temperatures", ...
    Path      string        // REST-Pfad, Platzhalter {hc}, {dhw}, {hs}
    Field     string        // JSON-Feld im Topic, z. B. "supplyTemperature"
    Name      string        // Anzeigename (deutsch), z. B. "Vorlauftemperatur"
    Kind      Kind          // Float, String, Bool, Enum, Map
    Component string        // "sensor" | "binary_sensor"
    DeviceClass, StateClass, Unit, Icon string
    MapKeys   map[string]string // bei Kind=Map: Quell-Key -> Feldname (z. B. "ch" -> "startsHeating")
    Translate map[string]string // optionale Übersetzung, z. B. forced -> "Winter"
    OnValue   string            // bei Bool: welcher String "an" bedeutet ("on", "active")
}
```

Bei `Kind=Map` werden Discovery und Feldnamen pro Key erzeugt.

### Gruppen und Werte (alle auf der Testanlage mit 200 bestätigt)

**`energy`** – Topics `…/heating`, `…/dhw`, `…/total`, `…/cooling` (bestehende Logik, `electricity` = compressor + eheater)

| Pfad | Topic |
|---|---|
| `/heatSources/emon/chConsumption` | `heating` |
| `/heatSources/emon/dhwConsumption` | `dhw` |
| `/heatSources/emon/totalConsumption` | `total` (neu) |
| `/heatSources/emon/coolingConsumption` | `cooling` (neu, optional) |

Wichtig: Sensor-IDs der bestehenden Energie-Sensoren (`heating_electricity`, …) **nicht ändern**, sonst verliert Home Assistant die Statistik.

**`power`** – Topic `…/power`

| Pfad | Feld | Einheit | HA |
|---|---|---|---|
| `/heatSources/compressor/powerElecActual` | `compressorPower` | W | sensor, power, measurement |
| `/heatSources/eHeater/powerElecActual` | `eheaterPower` | W | sensor, power, measurement |
| `/heatSources/actualModulation` | `modulation` | % | sensor, measurement |
| `/heatSources/hs1/refrigerant/compressorActualSpeed` | `compressorSpeed` | % | sensor, measurement |
| `/heatSources/hs1/pumpVolumeFlow` | `volumeFlow` | l/h | sensor, volume_flow_rate, measurement (Einheit in HA: `L/h`) |
| berechnet (Schritt 6) | `heatOutput` | W | sensor, power, measurement |
| berechnet (Schritt 6) | `cop` | – | sensor, measurement |

**`temperatures`** – Topic `…/temperatures`

| Pfad | Feld |
|---|---|
| `/heatSources/actualSupplyTemperature` | `supply` |
| `/heatSources/currentSupplySetpoint` | `supplySetpoint` |
| `/heatSources/returnTemperature` | `return` |
| `/heatSources/hs1/supplyFlowCondenserTemp` | `condenserSupply` |
| `/heatSources/additionalHeater/flowTemp` | `additionalHeaterFlow` |
| `/system/sensors/temperatures/outdoor_t1` | `outdoor` |
| `/heatSources/sensors/evaporatorTemp_tl2` | `evaporatorAir` |
| `/dhwCircuits/{dhw}/actualTemp` | `dhwActual` |
| `/dhwCircuits/{dhw}/currentSetpoint` | `dhwSetpoint` |

Alle: °C, `device_class: temperature`, `state_class: measurement`.

**`status`** – Topic `…/status_info` (`…/status` ist bereits online/offline)

| Pfad | Feld | HA |
|---|---|---|
| `/heatSources/hs1/refrigerant/status` | `heatPumpState` | enum (z. B. `heating`) |
| `/heatSources/actualHeatDemand` | `heatDemand` | enum/Text, Liste zu String verbinden (`ch`, `dhw`, …) |
| `/heatSources/flameStatus` | `compressorRunning` | binary_sensor, running, on=`on` |
| `/heatSources/Source/eHeater/status` | `eheaterActive` | binary_sensor, on=`on` |
| `/dhwCircuits/{dhw}/overallStatus` | `dhwStatus` | enum |
| `/dhwCircuits/{dhw}/currentTemperatureLevel` | `dhwLevel` | enum |
| `/dhwCircuits/{dhw}/tdrunningStatus` | `thermalDisinfection` | binary_sensor, on=`on` |
| `/dhwCircuits/{dhw}/chargeRemainingTime` | `dhwExtraRemaining` | sensor, duration, min |
| `/system/powerConstraints/silentMode/status` | `silentMode` | binary_sensor, on=`active` |
| `/system/powerConstraints/externalInputs/appliancePowerBlock/status` | `utilityBlock` | binary_sensor, on=`active` |
| `/system/powerLimitation/active` | `powerLimitation` | binary_sensor, on=`on` |
| `/heatSources/externalInputs/input1..4/state` | `externalInput1..4` | binary_sensor, on=`on` |

**`circuits`** – Topic `…/<hc>` pro Heizkreis (z. B. `…/hc1`, `…/hc2`); Name in HA: „HK1 …“

| Pfad | Feld | HA |
|---|---|---|
| `/heatingCircuits/{hc}/currentRoomSetpoint` | `roomSetpoint` | sensor, temperature |
| `/heatingCircuits/{hc}/maxFlowTemp` | `maxFlowTemperature` | sensor, temperature |
| `/heatingCircuits/{hc}/mixerPosition` | `mixerPosition` | sensor, % (nur HK2 – 404 bei HK1 → überspringen) |
| `/heatingCircuits/{hc}/currentSuWiMode` | `summerWinterMode` | enum, Übersetzung `forced`→Winter, `off`→Sommer, `cooling`→Kühlen |
| `/heatingCircuits/{hc}/overallStatus` | `status` | enum |
| `/heatingCircuits/{hc}/pumpStatus` | `pump` | binary_sensor, running, on=`on` |

Hinweis: Vorlauf-Ist/-Soll pro Heizkreis ist über die REST-Pfade **nicht** verfügbar (alle getesteten Varianten 404). Ggf. über `/signals` (Schritt 2).

**`maintenance`** – Topic `…/maintenance`

| Pfad | Feld(er) | HA |
|---|---|---|
| `/heatSources/hs1/numberOfStarts` | Map: `ch`→`startsHeating`, `dhw`→`startsDhw`, `cooling`→`startsCooling`, `total`→`startsTotal` | sensor, total_increasing |
| `/heatSources/hs1/workingTime` | Map (Sekunden): `workingTimeHeating`, `…Dhw`, `…Cooling`, `…Total` | sensor, duration, total_increasing, Einheit `s` (HA rechnet um) |
| `/heatSources/systemPressure` | `systemPressure` | sensor, pressure, bar |
| `/notifications` | `notificationCount` + `notifications` (Liste als JSON) | sensor, Anzahl; Attribute via `json_attributes_topic` |

**`refrigerant`** – Topic `…/refrigerant` (Default **aus**)

`compressorTemp`, `hotGasTemp`, `suctionGasTemp`, `highPressureTemp`, `lowPressureTemp`, `liquidPipeHeatingTemp`, `liquidPipeCoolingTemp` (alle `/heatSources/hs1/refrigerant/…`, °C), `oduFanSpeed` (`/heatSources/hs1/oduFanSpeed`, %), `heatCarrierPumpSpeed` (`/heatSources/hs1/refrigerant/heatCarrierPumpSpeed`, %).

## Schritt 5: Gruppen-Poller (`internal/app/group.go`, `internal/app/scheduler.go`)

1. `GroupPoller` pro Gruppe: hält die Definitionen der Gruppe, Intervall, Publisher, Fetcher, Logger.
2. `PollOnce`:
   - Alle Pfade der Gruppe abfragen (sequenziell reicht; das Gateway ist langsam und soll nicht überlastet werden).
   - Ergebnis je Topic in `map[string]any` sammeln, ungültige/fehlende Werte weglassen.
   - Bei `ErrNotFound`: Pfad in `skipped`-Set aufnehmen, einmal mit `Info` loggen, danach nicht mehr abfragen.
   - Andere Fehler: `Error` loggen, beim nächsten Intervall erneut.
   - Pro Topic ein JSON veröffentlichen (retain).
3. `Scheduler.Run(ctx)`: startet pro aktiver Gruppe eine Goroutine mit eigenem `time.Ticker`; wartet mit `sync.WaitGroup`, bis alle beendet sind (≈ mehrere `@Scheduled`-Methoden).
4. Zeitlich versetzter Start (z. B. 2 s Abstand pro Gruppe), damit nicht alle Gruppen gleichzeitig das Gateway abfragen.
5. Der bestehende `app.Poller` wird durch die `energy`-Gruppe ersetzt; Topics/Payloads bleiben kompatibel.

## Schritt 6: Berechnete Werte (`internal/app/derived.go`)

Wird in der Gruppe `power` nach dem Abruf berechnet. Benötigt zusätzlich `condenserSupply` und `return` (werden in der `power`-Gruppe mit abgefragt, auch wenn `temperatures` deaktiviert ist).

- Wärmeleistung [W] = `volumeFlow` [l/h] / 3600 × 4186 [J/(kg·K)] × (`condenserSupply` − `return`) [K]
  - Annahme: Dichte Wasser ≈ 1 kg/l.
  - Beispiel Testanlage: 1134 / 3600 × 4186 × (32,4 − 30,0) ≈ 3165 W.
- Live-COP = Wärmeleistung / (`compressorPower` + `eheaterPower`)
  - Beispiel: 3165 / 460 ≈ 6,9.
- Plausibilitätsregeln (dann Wert weglassen):
  - Verdichter aus (`compressorPower` < 50 W) → kein COP.
  - Temperaturdifferenz ≤ 0 oder > 20 K.
  - Volumenstrom ≤ 0.
  - COP > 15.
- Runden: Wärmeleistung auf ganze W, COP auf 2 Nachkommastellen.
- Hinweis in README: Näherungswert, Abtauphasen verfälschen ihn.

## Schritt 7: Home-Assistant-Discovery erweitern (`internal/mqtt/discovery.go`)

1. `Sensor` um `Component` (`sensor`/`binary_sensor`), `Options []string` (für `enum`), `PayloadOn`/`PayloadOff`, `JSONAttributesTopic`/`JSONAttributesTemplate` erweitern.
2. Topic: `<prefix>/<component>/<deviceID>/<key>/config`.
3. `binary_sensor`: `value_template` liefert `ON`/`OFF`, z. B. `{{ 'ON' if value_json.pump else 'OFF' }}` – dafür im JSON echte Booleans veröffentlichen (`"pump": true`).
4. `enum`: `device_class: enum`, `options: [...]` aus `allowedValues` bzw. Übersetzungstabelle.
5. Discovery nur für Werte senden, deren Gruppe aktiv ist. Für Werte, die mit 404 übersprungen werden, nach dem ersten Durchlauf eine **leere retained Nachricht** auf das Config-Topic senden (entfernt den Sensor in HA).
6. Optional: `entity_category: diagnostic` für Kältekreis, Wartung, Gateway-Werte.

## Schritt 8: Konfiguration (`internal/config`)

Neue Umgebungsvariablen:

| Variable | Default |
|---|---|
| `GROUP_ENERGY_ENABLED` / `GROUP_ENERGY_INTERVAL` | `true` / `5m` |
| `GROUP_POWER_ENABLED` / `GROUP_POWER_INTERVAL` | `true` / `1m` |
| `GROUP_TEMPERATURES_ENABLED` / `GROUP_TEMPERATURES_INTERVAL` | `true` / `1m` |
| `GROUP_STATUS_ENABLED` / `GROUP_STATUS_INTERVAL` | `true` / `1m` |
| `GROUP_CIRCUITS_ENABLED` / `GROUP_CIRCUITS_INTERVAL` | `true` / `5m` |
| `GROUP_MAINTENANCE_ENABLED` / `GROUP_MAINTENANCE_INTERVAL` | `true` / `15m` |
| `GROUP_REFRIGERANT_ENABLED` / `GROUP_REFRIGERANT_INTERVAL` | `false` / `1m` |
| `HEATING_CIRCUITS` | `hc1,hc2` |
| `DHW_CIRCUIT` | `dhw1` |
| `HEAT_SOURCE` | `hs1` |

- `POLL_INTERVAL` bleibt erhalten und dient als Fallback für `GROUP_ENERGY_INTERVAL` (Abwärtskompatibilität).
- Config bekommt `Groups map[string]GroupConfig{Enabled bool; Interval time.Duration}`.
- Validierung: Intervall ≥ 10 s, IDs nur `[a-z0-9]`.

## Schritt 9: Tests

- `buderus/value_test.go`: alle Antworttypen aus Anhang A (float, integer, string, Liste, Map), Filter für `-3276.8`/`32767`.
- `app/derived_test.go`: Beispielrechnung oben, alle Plausibilitätsregeln (table-driven).
- `app/group_test.go`: Fake-Fetcher mit 200/404/Fehler; prüfen, dass 404 nur einmal abgefragt wird und Fehler andere Werte nicht blockieren.
- `config_test.go`: Gruppen-Defaults, Fallback `POLL_INTERVAL`, ungültige Werte.
- `mqtt/discovery_test.go`: binary_sensor, enum, leere Config zum Entfernen.
- Vor jedem Commit: `make lint test`.

## Schritt 10: Doku

- README: Gruppen-Tabelle, neue Variablen, Topic-/Payload-Beispiele je Gruppe, Abschnitt „Skripte“ (`probe-all.sh`, `probe-signals.sh`, `probe-temperatures.sh`, `probe-heating-circuits.sh`), Hinweis zu COP-Näherung.
- `.env.example` um neue Variablen ergänzen (auskommentiert mit Defaults).
- Dieses Dokument: erledigte Schritte abhaken.

---

## Anhang A: Gemessene Antworten der Testanlage (Auszug, 2026-10-25)

```
/heatSources/actualSupplyTemperature      {"type":"floatValue","value":30.6,"unitOfMeasure":"C","state":[{"short":32767.0},{"open":-32768.0}]}
/heatSources/returnTemperature            {"value":30.0,"unit":"C"}
/heatSources/hs1/supplyFlowCondenserTemp  {"value":32.4,"unit":"C"}
/heatSources/compressor/powerElecActual   {"value":460.0,"unit":"W"}
/heatSources/hs1/pumpVolumeFlow           {"value":1134,"unit":"l/h"}
/heatSources/actualModulation             {"value":13.0,"unit":"%"}
/heatSources/actualHeatDemand             {"values":["ch"]}
/heatSources/hs1/refrigerant/status       {"value":"heating"}
/heatSources/flameStatus                  {"value":"on"}
/heatSources/hs1/numberOfStarts           {"values":[{"ch":737},{"dhw":194},{"cooling":0},{"total":931}]}
/heatSources/hs1/workingTime              {"values":[{"ch":19515812},{"dhw":1135643},{"cooling":0},{"total":20651456}],"unit":"s"}
/heatSources/systemPressure               {"value":1.7,"unit":"bar"}
/heatingCircuits/hc2/mixerPosition        {"type":"integerValue","value":49,"unitOfMeasure":"%"}
/heatingCircuits/hc1/currentSuWiMode      {"type":"stringValue","value":"forced","allowedValues":["forced","off","cooling"]}
/heatingCircuits/hc1/pumpStatus           {"type":"stringValue","value":"on","allowedValues":["off","on"]}
/dhwCircuits/dhw1/actualTemp              {"value":43.0,"unit":"C"}
/heatSources/emon/totalConsumption        {"values":[{"outputProduced":28180.42},{"eheater":1.29},{"compressor":7349.47}],"unit":"kWh"}
/notifications                            {"values":[]}
```

Vollständige Ausgabe neu erzeugen: `./scripts/probe-all.sh | tee probe-all.txt`

## Anhang B: Nicht verfügbar auf der Testanlage (404/403)

- `/heatingCircuits`, `/heatSources` (Übersichten, 403)
- Vorlauf pro Heizkreis: `/heatingCircuits/{hc}/actualSupplyTemperature`, `supplyTemperatureSetpoint`, `currentFlowSetpoint`, `flowTemperatureSetpoint`
- `/heatingCircuits/{hc}/roomtemperature` (kein Raumfühler)
- `/heatSources/hs1/returnFlowTemp`, `/heatSources/dhw/cylinderUpperTemp`, `/heatSources/pvContactState`
- `/system/variableTariff/*` (außer `supportStatus` = `not_supported`), `/zones/zone1/*`, Sole-Kreis (`brineCircuit`)

## Anhang C: Ergebnis `/signals`

_Noch offen – nach Schritt 1/2 ergänzen._
