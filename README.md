# Buderus-wp-to-mqtt

Ein Dienst, der die Energiezähler einer Buderus-Wärmepumpe über die lokale REST-API des Gateways (z. B. **MX400**, **K 40 RF**) abfragt und per **MQTT** veröffentlicht. Optional legt er die Sensoren per **Home-Assistant-MQTT-Discovery** automatisch an.

Aktuell werden abgefragt:

| Bereich | REST-Pfad | Werte |
|---|---|---|
| Heizung | `/heatSources/emon/chConsumption` | Stromverbrauch (gesamt, Verdichter, Zuheizer), erzeugte Wärme (kWh) |
| Warmwasser | `/heatSources/emon/dhwConsumption` | Stromverbrauch (gesamt, Verdichter, Zuheizer), erzeugte Wärme (kWh) |

Manche Gateways (z. B. MX400) liefern keinen Gesamt-Stromverbrauch (`electricity`), sondern nur `compressor` und `eheater`. In diesem Fall wird `electricity` als Summe der beiden berechnet.

> [!NOTE]
> **Inoffizielles Projekt.** Dieses Projekt steht in keiner Verbindung zu Bosch Thermotechnik GmbH oder Buderus und wird von diesen weder unterstützt noch geprüft. „Buderus“, „Bosch“ und „Logatherm“ sind Marken ihrer jeweiligen Inhaber.
>
> Der Dienst greift ausschließlich **lesend** auf die lokale API zu. Die Nutzung erfolgt auf eigene Verantwortung und ohne Gewähr – insbesondere für die Richtigkeit der Messwerte (z. B. für Abrechnungszwecke).

### Getestete Hardware

| Komponente | Version |
|---|---|
| Wärmepumpe | Buderus Logatherm WLW186i-12 E (Luft/Wasser) |
| Gateway | Buderus MX400 (meldet sich als `K40RF_v1`), Firmware `15.00.01` |
| Bedieneinheit | Logamatic BC400 (Systembus EMS 2.0) |

Andere Geräte mit der *K 40 RF Local API* sollten ebenfalls funktionieren; Rückmeldungen dazu gern als Issue.

## Schnellstart (Docker Compose)

Das Image wird bei jedem Push und Release nach `ghcr.io/patrickhofmann0/buderus-wp-to-mqtt` gepusht (siehe [Release & Deployment](#release--deployment)).

1. Token holen: siehe [authenticate.md](authenticate.md). Der Token läuft nicht ab.
2. Repository bzw. `docker-compose.yml` und `.env.example` auf den Server kopieren und Konfiguration anlegen:
   ```sh
   cp .env.example .env
   # .env bearbeiten: BUDERUS_HOST, BUDERUS_TOKEN, MQTT_BROKER, ggf. MQTT_USERNAME/MQTT_PASSWORD, IMAGE_TAG
   ```
3. Einmalig an der Registry anmelden (solange das Image privat ist, siehe unten).
4. Starten:
   ```sh
   docker compose pull
   docker compose up -d
   docker compose logs -f
   ```

## Konfiguration

Alle Einstellungen werden über Umgebungsvariablen gesetzt.

| Variable | Pflicht | Default | Beschreibung |
|---|---|---|---|
| `BUDERUS_HOST` | ja | – | Hostname oder IP des Gateways |
| `BUDERUS_TOKEN` | ja | – | Access-Token aus [authenticate.md](authenticate.md) |
| `BUDERUS_PORT` | | `9443` | Port der REST-API |
| `MQTT_BROKER` | ja | – | z. B. `tcp://192.168.1.10:1883` oder `ssl://…:8883` |
| `MQTT_USERNAME` | | – | Benutzername am Broker |
| `MQTT_PASSWORD` | | – | Passwort am Broker |
| `MQTT_TOPIC_PREFIX` | | `buderus/wp` | Präfix für alle Topics; `/` am Anfang/Ende wird entfernt |
| `MQTT_DISCOVERY_ENABLED` | | `true` | Home-Assistant-Discovery senden (`false` für reine MQTT-Nutzung) |
| `MQTT_HA_DISCOVERY_PREFIX` | | `homeassistant` | Discovery-Präfix von Home Assistant |
| `DEVICE_NAME` | | `Buderus Wärmepumpe` | Anzeigename des Geräts |
| `DEVICE_ID` | | aus `DEVICE_NAME` abgeleitet | Stabile ID (`[a-z0-9_]`); setzen, wenn der Name sich ändern darf |
| `POLL_INTERVAL` | | `5m` | Abfrageintervall (Go-Duration, z. B. `30s`, `5m`, `1h`; min. `10s`) |
| `LOG_LEVEL` | | `info` | `debug`, `info`, `warn`, `error` |

Die TLS-Prüfung zum Gateway ist deaktiviert, da es ein selbstsigniertes Zertifikat verwendet (entspricht `curl --insecure`).

## MQTT-Topics

Beispiel mit `MQTT_TOPIC_PREFIX=buderus/wp`:

| Topic | Inhalt | Retained |
|---|---|---|
| `buderus/wp/status` | `online` / `offline` (Last Will) | ja |
| `buderus/wp/heating` | `{"electricity":6265.99,"compressor":6265.99,"eheater":0,"outputProduced":24797.11,"unit":"kWh"}` | ja |
| `buderus/wp/dhw` | `{"electricity":1100.5,"compressor":1100.5,"eheater":0,"outputProduced":3382.54,"unit":"kWh"}` | ja |

| Feld | Bedeutung |
|---|---|
| `electricity` | Gesamter Stromverbrauch (vom Gateway oder berechnet aus `compressor` + `eheater`) |
| `compressor` | Stromverbrauch des Verdichters |
| `eheater` | Stromverbrauch des elektrischen Zuheizers |
| `burner` | Brenner (nur Hybridsysteme) |
| `outputProduced` | Erzeugte Wärme |

Fehlt ein Wert in der Antwort des Gateways, wird das Feld weggelassen. Mit `LOG_LEVEL=debug` wird die Rohantwort des Gateways geloggt.

### Home Assistant

Bei `MQTT_DISCOVERY_ENABLED=true` werden beim Start pro Bereich (`heating`, `dhw`) vier Sensoren angelegt, gruppiert unter einem Gerät:

| Sensor-Key | Name |
|---|---|
| `<bereich>_electricity` | Stromverbrauch |
| `<bereich>_compressor` | Strom Verdichter |
| `<bereich>_eheater` | Strom Zuheizer |
| `<bereich>_output_produced` | erzeugte Wärme |

Discovery-Topic: `<MQTT_HA_DISCOVERY_PREFIX>/sensor/<DEVICE_ID>/<sensor-key>/config`

Die Sensoren haben `device_class: energy` und `state_class: total_increasing` und können direkt im **Energie-Dashboard** verwendet werden.

## Lokale Entwicklung

### Voraussetzungen

- [Go 1.27](https://go.dev/dl/) (`go version` zur Kontrolle)
- [golangci-lint v2](https://golangci-lint.run/welcome/install/) für Linting und Formatierung
- Docker (für lokalen MQTT-Broker und Image-Build)
- optional `make` und `mosquitto-clients` (`mosquitto_sub`)

Als IDE eignen sich GoLand (IntelliJ-basiert, für Java-Entwickler vertraut) oder VS Code mit der Go-Extension.

### Ablauf

```sh
# 1. Abhängigkeiten laden (≈ mvn dependency:resolve)
go mod download

# 2. Lokalen MQTT-Broker starten (ohne Authentifizierung)
docker compose -f docker-compose.dev.yml up -d mosquitto

# 3. Konfiguration anlegen
cp .env.example .env
# In .env: MQTT_BROKER=tcp://localhost:1883 sowie Gateway-Host und Token eintragen

# 4. In einem zweiten Terminal alle Nachrichten mitlesen
mosquitto_sub -h localhost -t 'buderus/#' -t 'homeassistant/#' -v

# 5. Programm starten (lädt .env)
make run
```

Tipp: Mit `POLL_INTERVAL=30s` und `LOG_LEVEL=debug` sieht man schneller Ergebnisse.

### Befehle

| Make | Ohne Make | Zweck |
|---|---|---|
| `make build` | `go build -o bin/buderus2mqtt ./cmd/buderus2mqtt` | Binary bauen (≈ `mvn package`) |
| `make run` | `set -a; . ./.env; set +a; go run ./cmd/buderus2mqtt` | Lokal starten |
| `make test` | `go test -race -cover ./...` | Unit-Tests (≈ `mvn test`) |
| `make lint` | `golangci-lint run` | Statische Analyse |
| `make fmt` | `golangci-lint fmt` | Code formatieren |
| `make tidy` | `go mod tidy` | `go.mod`/`go.sum` aufräumen |
| `make docker` | `docker build --build-arg VERSION=dev -t buderus2mqtt:dev .` | Docker-Image lokal bauen |

Einzelnen Test ausführen: `go test ./internal/config -run TestSlugify -v`

### Ohne lokale Go-Installation

Das Docker-Image führt beim Bauen die Tests aus. Für Tests ohne Image:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./...
```

Broker und Programm zusammen als Container (Image wird lokal gebaut):

```sh
# in .env: MQTT_BROKER=tcp://mosquitto:1883
docker compose -f docker-compose.dev.yml up -d --build
```

## Release & Deployment

Der Workflow [`.github/workflows/build.yml`](.github/workflows/build.yml) läuft bei jedem Push, jedem Versions-Tag und jedem Pull Request:

| Ereignis | Lint & Test | Image bauen & pushen | GitHub Release |
|---|---|---|---|
| Pull Request | ✔ | – | – |
| Push auf einen Branch | ✔ | ✔ | – |
| Tag `vMAJOR.MINOR.PATCH` | ✔ | ✔ | ✔ |

Images werden für `linux/amd64` und `linux/arm64` gebaut und nach `ghcr.io/patrickhofmann0/buderus-wp-to-mqtt` gepusht.

### Release erstellen

```sh
git tag v0.1.0
git push origin v0.1.0
```

Fortschritt: GitHub → *Actions*. Die Version wird ins Binary eingebaut und beim Start geloggt (`started version=0.1.0 …`, bei Branch-Builds `version=sha-a1b2c3d`).

### Image-Tags

| Ereignis | Image-Tags |
|---|---|
| Push auf `main` (Commit `a1b2c3d`) | `main`, `edge`, `sha-a1b2c3d` |
| Push auf `feature/xyz` | `feature-xyz`, `sha-a1b2c3d` |
| Tag `v0.1.0` | `0.1.0`, `0.1`, `latest`, `sha-a1b2c3d` |
| Tag `v1.2.3` | `1.2.3`, `1.2`, `1`, `latest`, `sha-…` |
| Tag `v1.3.0-rc.1` | `1.3.0-rc.1`, `sha-…` (Pre-Release, `latest` bleibt unverändert) |

- `latest` = letzte stabile Version, `edge` = neuester Stand von `main`.
- Bei `0.x`-Versionen wird kein Major-Tag (`0`) vergeben, da laut SemVer jede Minor-Version inkompatibel sein darf.
- `/` im Branch-Namen wird zu `-`.

Die Version auf dem Server wird über `IMAGE_TAG` in der `.env` festgelegt (Default `latest`). Zum Testen eines Standes z. B. `IMAGE_TAG=edge` oder `IMAGE_TAG=sha-a1b2c3d`.

### Zugriff auf das private Image

Solange das Package privat ist, braucht der Server ein Login:

1. GitHub → *Settings → Developer settings → Personal access tokens (classic)* → Token mit Berechtigung **`read:packages`** anlegen.
2. Auf dem Server einmalig:
   ```sh
   echo <TOKEN> | docker login ghcr.io -u patrickhofmann0 --password-stdin
   ```

Öffentlich machen: GitHub → *Packages → buderus-wp-to-mqtt → Package settings → Change visibility*.

### Update auf dem Server

```sh
docker compose pull
docker compose up -d
```

## Projektstruktur

```
cmd/buderus2mqtt/main.go     Einstiegspunkt: Config laden, Objekte verdrahten, Poller starten
internal/config/             Umgebungsvariablen lesen und validieren
internal/buderus/            REST-Client und JSON-Modelle des Gateways
internal/mqtt/               MQTT-Verbindung (Paho) und Home-Assistant-Discovery
internal/app/                Poller: Werte abfragen und veröffentlichen
```

`internal/` ist ein Go-Mechanismus: Diese Packages können nur innerhalb dieses Moduls importiert werden.

## Go für Java-Entwickler

| Java | Go in diesem Projekt |
|---|---|
| `pom.xml` / Maven | `go.mod`, `go.sum`, `go` CLI |
| Klasse | `struct` + Methoden, z. B. `buderus.Client` |
| Konstruktor | Funktion `NewXxx(...)`, z. B. `buderus.NewClient` |
| `private` / `public` | kleiner / großer Anfangsbuchstabe |
| `implements` | implizit: wer die Methoden hat, erfüllt das Interface (`app.Publisher`) |
| Exceptions | Fehler als letzter Rückgabewert: `value, err := f()`; `if err != nil {…}` |
| Exception-Chaining | `fmt.Errorf("context: %w", err)` |
| `finally` / try-with-resources | `defer` |
| Jackson `@JsonProperty` | Struct-Tags `json:"name"` |
| Eigener `JsonDeserializer` | `UnmarshalJSON` (siehe `buderus.EmonValues`) |
| Dependency Injection | Abhängigkeiten im Konstruktor übergeben, Verdrahtung in `main.go` |
| `@Scheduled` | `time.Ticker` in `app.Poller.Run` |
| Thread-Interrupt / Cancel | `context.Context` |
| JUnit `@ParameterizedTest` | Table-driven Tests mit `t.Run` |
| Mockito | Handgeschriebene Fakes, die das Interface erfüllen |
| MockWebServer | `httptest.NewTLSServer` |
| SLF4J | `log/slog` |

## Weitere Dokumentation

- [authenticate.md](authenticate.md) – Access-Token vom Gateway holen
- [energy.md](energy.md) – Energiezähler per `curl` abfragen
- [buderus.yml](buderus.yml) – OpenAPI-Spezifikation der lokalen API

## Lizenz

Der Code dieses Projekts steht unter der [MIT-Lizenz](LICENSE.md).

Ausnahme: [buderus.yml](buderus.yml) ist die OpenAPI-Spezifikation der *K 40 RF Local API* von **Bosch Thermotechnik GmbH** ([bosch-home-comfort/api-docs](https://github.com/bosch-home-comfort/api-docs)) und steht unter der [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0). Details siehe [NOTICE](NOTICE).
