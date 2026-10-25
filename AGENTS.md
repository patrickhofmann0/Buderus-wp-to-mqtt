# Entwicklungsregeln

## Projektzweck

Der ressourcenschonende Container-Service fragt Daten einer Buderus-Wärmepumpe über REST ab, bildet die Antworten auf MQTT-Topics ab und sendet sie an einen Broker. Der Datenfluss lautet: ENV-Konfiguration → Zeitplanung → REST-Abruf → Mapping → MQTT-Veröffentlichung. Gerätespezifische API-Endpunkte werden konfiguriert, nicht automatisch erkannt.

## Verbindliche Pflege dieser Datei

> **Bei JEDER fachlichen oder architektonischen Änderung MUSS `AGENTS.md` im selben Commit beziehungsweise derselben PR aktualisiert werden.** Neue Verantwortlichkeiten, Betriebsannahmen, Schnittstellen und Invarianten hier nachvollziehbar beschreiben. Diese Pflicht ist Teil der Änderung, kein späteres Follow-up.
>
> **Bei Konfigurationsänderungen zusätzlich `README.md` und `.env.example` im selben Commit beziehungsweise derselben PR aktualisieren.** Standardwerte, Validierung und Beispiele müssen mit dem Code übereinstimmen.

Keine zusätzlichen Markdown-Dateien für Pläne, Statusberichte oder redundante Dokumentation anlegen. Dauerhafte Nutzerdokumentation gehört in `README.md`, Beitrags- und Architekturregeln in diese Datei.

## Sprache und Stil

- README und Entwicklungsregeln auf Deutsch pflegen.
- Go-Schnittstellen, Bezeichner, GoDoc, Fehlermeldungen und Code auf Englisch halten.
- Idiomatisches Go, kleine Funktionen, explizite Fehlerbehandlung und klare Paketgrenzen bevorzugen.
- Nur erklärungsbedürftige Entscheidungen kommentieren; offensichtlichen Code nicht nacherzählen.
- Änderungen auf die Aufgabe beschränken; keine beiläufigen Umstrukturierungen oder Reparaturen.

## Go-Konventionen

- Die Standardbibliothek bevorzugen; externe Abhängigkeiten nur bei belegbarem Nutzen hinzufügen.
- Paketnamen kurz und kleingeschrieben halten; keine Unterstriche oder redundanten Namen. Bezeichner kurz, eindeutig und idiomatisch wählen; Getter ohne `Get`-Präfix benennen.
- Fehler mit Kontext und `%w` weiterreichen; zur Prüfung `errors.Is` und `errors.As` statt Stringvergleich verwenden. Fehlermeldungen beginnen kleingeschrieben und enden ohne Punkt.
- Fehler entweder auf der zuständigen Ebene protokollieren oder zurückgeben, nicht beides. Keine `panic`-Aufrufe für erwartbare Konfigurations-, Netzwerk- oder Datenfehler.
- Kleine Interfaces beim konsumierenden Code definieren: Interfaces annehmen, konkrete Structs zurückgeben. Keine spekulativen Abstraktionen oder Interfaces nur zur später möglichen Erweiterung.
- Für optionale Konstruktorparameter das bestehende Functional-Options-Muster nutzen; erforderliche Parameter explizit lassen und Optionen beim Erzeugen validieren.
- `context.Context` ist der erste Funktionsparameter für abbrechbare Arbeit. Kontexte nicht in Struct-Feldern speichern; an Aufrufe weiterreichen und Cancel-Funktionen zuverlässig ausführen.
- Einen eigenen wiederverwendbaren `http.Client` mit Timeout verwenden, nicht unbeschränkt `http.Get`. Antwortgrößen mit `io.LimitReader` und zusätzlichem Prüfbyte begrenzen; Bodies zuverlässig schließen.
- Jede Goroutine benötigt einen klaren Besitzer, begrenzte Blockierungen und einen nachvollziehbaren Abbruch-/Shutdown-Lebenszyklus. Keine verwaisten Goroutinen oder unbeschränkten Warteschlangen.
- Table-driven Tests verwenden kleine Fakes und `httptest.NewServer` statt Mock-Frameworks. Verhalten und Fehlerpfade prüfen, nicht unnötig interne Aufrufsequenzen festschreiben.

## Technologie und Architektur

- Auslieferung ausschließlich als Docker-Container über GHCR, nicht als Go-Bibliothek. Go-Version: **1.27.3**; der Modulpfad `github.com/patrickhofmann0/Buderus-wp-to-mqtt` ist eine technische Build-Voraussetzung.
- `cmd/buderus-wp-to-mqtt/main.go` übernimmt Einstieg und Wiring. Die Laufzeit orchestriert Startabfrage, Cron, Signalbehandlung und geordnetes Beenden.
- `config` lädt und validiert ausschließlich ENV-Konfiguration. Die Binärdatei enthält keinen `.env`-Loader.
- `restclient` kapselt HTTP-GET, Authentifizierung, Timeouts, TLS und begrenztes Lesen.
- `mapper` übersetzt Antwortbytes in `Message`-Werte. `Mapper`, `Func`, `JSONFields` und `Passthrough` bleiben unabhängig von Transport und Scheduler.
- `mqttpub` kapselt Verbindung, Reconnect, Publish, Last Will und Verfügbarkeitsstatus.
- Verzeichnisse und Go-Pakete sind Implementierungsdetails ohne stabile unterstützte Library-API. Bestehende Paketpfade bleiben aus Gründen des Änderungsumfangs bestehen; weder externe Bibliotheksnutzer noch deren Kompatibilitätsanforderungen voraussetzen. Paketgrenzen dienen der internen Wartbarkeit und Testbarkeit, nicht einer Bibliotheksveröffentlichung.
- Kein CLI-Wiring und keine impliziten Umgebungszugriffe in Transport- oder Mapping-Pakete verlagern.
- HTTP und MQTT erhalten abbrechbare Kontexte beziehungsweise begrenzte Wartezeiten; keine unkontrollierten Hintergrund-Goroutinen.

## Konfigurationsvertrag

- `API_URLS` hat nur bei nichtleerem Wert Vorrang vor `API_URL`. Ein leerer oder nicht gesetzter Wert fällt auf `API_URL` zurück.
- Endpunktnamen sind eindeutig und entsprechen `[A-Za-z0-9_-]+`. Nur absolute HTTP(S)-URLs ohne eingebettete Zugangsdaten oder Fragment akzeptieren.
- Bearer-Token und Basic Auth schließen sich gegenseitig aus. Authentifizierung, Timeout und Größenlimit gelten für alle Endpunkte.
- Standardwerte: HTTP `15s`, Antwortlimit `1048576` Bytes; Broker `tcp://localhost:1883`, Client-ID `buderus-wp-to-mqtt`, Topic `buderus/wp`, QoS `0`, Retain `false`, MQTT-Timeout `5s`, Availability `true`.
- `KeepAlive` ist eine interne MQTT-Konfiguration mit Standard `30s`, keine zusätzliche ENV-Variable. `AvailabilityTopic` ist ebenfalls Bestandteil der internen MQTT-Konfiguration.
- Standardmapping `fields`, Standardplan `@every 60s`, Startabfrage `true`, Zeitzone `UTC`, Log-Level `info`, Log-Format `json`.
- Dauerwerte müssen positiv sein; QoS ist `0` bis `2`. Log-Level: `debug/info/warn/error`; Log-Format: `json/text`.
- Keine versteckten Schalter, stillen Fallbacks bei ungültigen Werten oder unbeabsichtigten neuen ENV-Variablen hinzufügen.
- `.env.example` muss sowohl mit Compose `env_file` als auch mit Docker `--env-file` funktionieren. Dauer-/Cron-Werte mit Leerzeichen nicht quoten, da Docker Quotes wörtlich übernimmt. Die Datei ist kein Shell-Skript; Shell-Exports müssen separat quotiert werden.

## REST- und Mapping-Invarianten

- `restclient.New` und Optionen validieren Eingaben vor Netzwerkzugriffen. Optionen umfassen Timeout, maximale Body-Größe, Bearer, Basic Auth, Header, eigenen HTTP-Client und explizite unsichere TLS-Ausnahme.
- `Fetch(ctx)` schließt Response-Bodies. Nicht-2xx liefert `*StatusError` mit Statuscode; Antwortdaten dürfen dabei nicht in Fehlermeldungen landen.
- Größenüberschreitung durch begrenztes Lesen mit einem zusätzlichen Prüfbyte erkennen. Niemals einen abgeschnittenen Body als erfolgreich behandeln.
- JSONFields akzeptiert genau ein JSON-Objekt, sortiert Schlüssel rekursiv und flacht verschachtelte Objekte zu Topic-Pfaden ab.
- Zahlenpräzision erhalten: `json.Number` statt implizitem `float64`. Strings roh, Arrays als JSON, `null` als Text `null` übertragen.
- `/`, `+`, `#`, Leerzeichen und NUL in Schlüsseln durch `_` ersetzen, leere Schlüssel durch `_`. Topic-Kollisionen müssen das Mapping als Ganzes ablehnen.
- Passthrough veröffentlicht unveränderte Antwortbytes. Nachrichten tragen ihr eigenes Retain-Flag.
- Einzelendpunkt: `MQTT_TOPIC[/feld]`; mehrere Endpunkte: `MQTT_TOPIC/name[/feld]`.
- `MQTT_TOPIC/status` ist bei aktiver Availability reserviert. Die Anwendung lehnt Mapping-Ergebnisse mit einer Nachricht auf exakt `MQTT.AvailabilityTopic` vor der Veröffentlichung ab; der Publisher weist Datenmeldungen auf sein `AvailabilityTopic` ebenfalls ab. Daten dürfen den Verfügbarkeitsstatus nicht überschreiben.

## Laufzeit und Betrieb

- Fünffeldrige Cron-Ausdrücke ohne Sekundenfeld und Standarddeskriptoren unterstützen; Intervalle müssen positiv sein.
- Überlappende Durchläufe überspringen, nicht parallel starten oder unbegrenzt nachholen. Startabfrage und Scheduler müssen denselben Ausschluss beachten.
- IANA-Zeitzonendaten mit `time/tzdata` einbetten, damit das minimale Image keine externe Zonen-Datenbank benötigt.
- SIGINT/SIGTERM muss laufende Arbeit kontrolliert beenden und MQTT sauber schließen.
- `slog` auf stdout, strukturiertes JSON standardmäßig, optional Text. Fehler mit Kontext protokollieren, ohne Geheimnisse preiszugeben.
- Availability veröffentlicht retained `online` bei Verbindung und Reconnect; geordnetes Beenden und Last Will liefern retained `offline`.
- Für `online`, `offline` und Last Will gilt QoS `min(Daten-QoS, 1)`; Daten behalten die konfigurierte QoS `0/1/2`. Status-Replay muss eine vollständige PUBLISH-Meldung bleiben: Ein wiederholtes QoS-2-PUBREL kann den nach Last Will gespeicherten `offline`-Status nicht durch `online` ersetzen.
- Daten-Publish nur bei offener MQTT-Verbindung zulassen, nicht während Reconnect. Höchstens eine Datenmeldung darf gleichzeitig eingereiht beziehungsweise unbestätigt sein, einschließlich eines nach Kontextabbruch noch laufenden Enqueue-Vorgangs; weitere Publikationen bei ausstehender Zustellung mit `ErrPending` abweisen, keine unbegrenzte Offline-Warteschlange aufbauen.
- MQTT-Timeout und Kontextabbruch beenden nur das Warten, nicht zwingend die spätere Zustellung. Die Pending-Sperre erst nach tatsächlicher Broker-Bestätigung lösen; beim Paho-Replay können Tokens ersetzt oder vorzeitig abgeschlossen werden, daher Persistenz-IDs berücksichtigen.
- Availability besitzt einen separaten begrenzten Pfad: ausstehendes `online` bei Reconnect wiederverwenden, `offline` vor begrenztem Disconnect bestmöglich senden. Beide Pfade müssen Speicher und Shutdown-Zeit beschränken.
- Broker-Verfügbarkeit ist kein Nachweis erfolgreicher REST-Abfragen. Keine scheinbare Healthcheck-Funktion einführen, die nur Konfiguration prüft. Ein echter externer Laufzeitcheck benötigt Heartbeat oder persistenten Erfolgszustand.
- Container-Prozessende, Restart-Policy, Brokerstatus und externe Überwachung der Datenaktualität sind die vorgesehenen Betriebssignale.

## Sicherheit

- Keine Tokens, Passwörter, echten `.env`-Dateien, sensiblen URLs oder Antworten committen oder protokollieren.
- Zugangsdaten per ENV übergeben; Beispiele verwenden ausschließlich offensichtliche Platzhalter beziehungsweise lokale Beispieladressen.
- TLS-Prüfung standardmäßig aktiv. `API_INSECURE_SKIP_VERIFY` nur als ausdrücklich dokumentierte lokale Ausnahme für selbstsignierte Geräte behandeln; MITM-Risiko deutlich nennen.
- HTTP und unverschlüsseltes MQTT nicht als sicher darstellen. Über fremde Netze HTTPS und MQTT-TLS nutzen.
- URLs, Authentifizierung, Header, Topics und Antwortgrößen früh validieren. Übergebene HTTP-Clients nicht durch unerwartete globale Mutationen beeinflussen.
- Vor neuen Abhängigkeiten Sicherheitslage und Notwendigkeit prüfen; anschließend bestehende Schwachstellenprüfung verwenden.

## Tests und Qualität

- Bestehende Tests und Werkzeuge nutzen; für eine Änderung keine unnötigen neuen Test-, Lint- oder Build-Systeme hinzufügen.
- Zuerst die kleinste relevante Testauswahl ausführen. Zusammengehörige Tests desselben Runners bündeln; erst bei Bedarf erweitern.
- HTTP-Tests mit lokalen Testservern, Mapping-Tests ohne Netzwerk und MQTT-Tests mit kontrollierter Broker-/Client-Umgebung durchführen. Keine reale Wärmepumpe und keinen öffentlichen Broker voraussetzen.
- Erfolgsfälle und Fehlerpfade abdecken: ungültige ENV, Auth-Konflikte, StatusError, Body-Limit, Abbruch, Präzision, Sortierung, Sanitisierung, Kollisionen, Publish-Fehler, Reconnect und Shutdown.
- Vor Code-PRs vorhandene Prüfungen berücksichtigen:
  ```sh
  go build ./...
  go vet ./...
  go test ./... -race -cover
  golangci-lint run
  govulncheck ./...
  ```
- `gofmt` und `goimports` für Go-Code verwenden. `go mod tidy` bei Abhängigkeitsänderungen; `go.mod` und `go.sum` gemeinsam prüfen.
- Keine Abhängigkeiten installieren, solange weder Manifeständerung noch ein fehlendes Werkzeug die Installation erfordert.
- Dokumentationsänderungen allein benötigen keine Tests oder Builds. Nicht ausgeführte Prüfungen und tatsächliche Einschränkungen ehrlich benennen.

## Docker und Deployment

- Statischer Build, Laufzeit auf `scratch`, CA-Zertifikate verfügbar, Non-root UID/GID `65534`. Keine Shell oder Healthcheck-Binärdatei im Image voraussetzen.
- Compose-Service `buderus-wp-to-mqtt`: Image `ghcr.io/patrickhofmann0/buderus-wp-to-mqtt:latest`, `build: .`, `env_file: .env`, `restart: unless-stopped`, 32 MB RAM, 0,1 CPU, schreibgeschütztes Dateisystem.
- Optionale Mosquitto-Instanz nur im Profil `dev`; `deploy/mosquitto.conf` einbinden, Host-Port 1883 nur an Loopback veröffentlichen. Anonymer Zugriff ist ausschließlich für lokale Entwicklung.
- Im Container bezeichnet `localhost` nicht den Host. Der Dev-Broker wird mit `MQTT_BROKER=tcp://mosquitto:1883` angesprochen.
- Größen- und RAM-Angaben sind Schätzungen, keine Zusagen. Architektur, Versionen, TLS und große Antworten berücksichtigen.

## Änderungen, CI und Reviews

- Die bestehende CI unter `.github/workflows/ci.yml` und vorhandene Release-/Containerabläufe respektieren.
- Prüfwerkzeuge mit derselben Go-Toolchain wie den Service bauen. Die CI installiert golangci-lint aus Quellcode und nutzt nach `setup-go` `GOTOOLCHAIN=local`, damit ein älter gebautes Werkzeug die aktuelle Go-Version nicht ablehnt.
- Neue Konfiguration, Schnittstellen oder geändertes Fehlerverhalten mit passenden Tests und aktualisierter Dokumentation liefern.
- Keine Geheimnisse oder generierten Binärdateien einchecken. Diffs vor dem Commit auf unbeabsichtigte Änderungen prüfen.
- PR-Beschreibungen nennen Zweck, relevante Verhaltensänderungen, ausgeführte Prüfungen und verbleibende Einschränkungen.
- Fachliche und architektonische Entscheidungen sind erst vollständig umgesetzt, wenn **diese Datei im selben Commit/PR aktualisiert** wurde; Konfigurationsänderungen benötigen zudem README und ENV-Beispiel.
