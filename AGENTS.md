# Entwicklungsregeln

## Projektzweck

Zwei unabhängig baubare Container-Implementierungen fragen Daten einer Buderus-Wärmepumpe über REST ab, bilden die Antworten auf MQTT-Topics ab und senden sie an einen Broker: Go unter `go/` und Quarkus Native unter `quarkus/`. Sie stehen für spätere Funktions- und Ressourcenvergleiche nebeneinander; Quarkus ersetzt Go nicht. Der gemeinsame Datenfluss lautet: ENV-Konfiguration → Zeitplanung → REST-Abruf → Mapping → MQTT-Veröffentlichung. Gerätespezifische API-Endpunkte werden konfiguriert, nicht automatisch erkannt.

## Verbindliche Pflege dieser Datei

> **Bei JEDER fachlichen oder architektonischen Änderung MUSS `AGENTS.md` im selben Commit beziehungsweise derselben PR aktualisiert werden.** Neue Verantwortlichkeiten, Betriebsannahmen, Schnittstellen und Invarianten hier nachvollziehbar beschreiben. Diese Pflicht ist Teil der Änderung, kein späteres Follow-up.
>
> **Bei Konfigurationsänderungen zusätzlich `README.md` und `.env.example` im selben Commit beziehungsweise derselben PR aktualisieren.** Standardwerte, Validierung und Beispiele müssen mit dem Code übereinstimmen.

Keine zusätzlichen Markdown-Dateien für Pläne, Statusberichte oder redundante Dokumentation anlegen. Dauerhafte Nutzerdokumentation gehört in `README.md`, Beitrags- und Architekturregeln in diese Datei.

## Sprache und Stil

- README und Entwicklungsregeln auf Deutsch pflegen.
- Go-/Java-Schnittstellen, Bezeichner, GoDoc/JavaDoc, Fehlermeldungen und Code auf Englisch halten.
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

- Auslieferung beider Varianten ausschließlich als Docker-Container über GHCR, nicht als Bibliotheken. Go-Version: **1.27.3**; `go/go.mod` behält den Modulpfad `github.com/patrickhofmann0/Buderus-wp-to-mqtt` als technische Build-Voraussetzung.
- `go/cmd/buderus-wp-to-mqtt/main.go` übernimmt Einstieg und Wiring. Die Laufzeit orchestriert Startabfrage, Cron, Signalbehandlung und geordnetes Beenden.
- `go/config` lädt und validiert ausschließlich ENV-Konfiguration. Die Anwendungen enthalten keinen `.env`-Loader.
- `go/restclient` kapselt HTTP-GET, Authentifizierung, Timeouts, TLS und begrenztes Lesen.
- `go/mapper` übersetzt Antwortbytes in `Message`-Werte. `Mapper`, `Func`, `JSONFields` und `Passthrough` bleiben unabhängig von Transport und Scheduler.
- `go/mqttpub` kapselt Verbindung, Reconnect, Publish, Last Will und Verfügbarkeitsstatus.
- `go/Dockerfile`, `go/.dockerignore` und `go/.golangci.yml` gehören zum Go-Projekt; Go-Befehle im Verzeichnis `go/` ausführen. Keine zweite Kopie des Go-Moduls im Repository-Wurzelverzeichnis pflegen.
- `quarkus/pom.xml`, `quarkus/src/` und `quarkus/Dockerfile` bilden das unabhängige Quarkus-Projekt. Java-Konfiguration, HTTP, Mapping, MQTT und Zeitplanung getrennt halten; native Laufzeit ohne JVM ausliefern.
- Quarkus-Version **3.40.1**, Java-Release **21**, Maven-Projekt. Der native Docker-Build benötigt Mandrel **25** (`jdk-25`), nicht Mandrel 21; keine Go-Toolchain dafür voraussetzen. Quarkus läuft als Hintergrundanwendung ohne HTTP-Server.
- Gemeinsame Nutzerkonfiguration und Deployment bleiben im Wurzelverzeichnis: `.env.example`, `docker-compose.yml`, `deploy/mosquitto.conf`, `README.md` und diese Datei. Keine konkurrierenden ENV-Vorlagen je Implementierung anlegen.
- Verzeichnisse und Go-Pakete sind Implementierungsdetails ohne stabile unterstützte Library-API. Bestehende Paketpfade bleiben aus Gründen des Änderungsumfangs bestehen; weder externe Bibliotheksnutzer noch deren Kompatibilitätsanforderungen voraussetzen. Paketgrenzen dienen der internen Wartbarkeit und Testbarkeit, nicht einer Bibliotheksveröffentlichung.
- Kein CLI-Wiring und keine impliziten Umgebungszugriffe in Transport- oder Mapping-Pakete verlagern.
- HTTP und MQTT erhalten abbrechbare Kontexte beziehungsweise begrenzte Wartezeiten; keine unkontrollierten Hintergrund-Goroutinen.

## Konfigurationsvertrag

- Der gemeinsame ENV-Vertrag gilt für beide Anwendungen. Tatsächliche Implementierungsunterschiede in der README offenlegen und testen; keine unbewiesene vollständige Verhaltens- oder Leistungsparität behaupten.
- `API_URLS` hat nur bei nichtleerem Wert Vorrang vor `API_URL`. Ein leerer oder nicht gesetzter Wert fällt auf `API_URL` zurück.
- Endpunktnamen sind eindeutig und entsprechen `[A-Za-z0-9_-]+`. Nur absolute HTTP(S)-URLs ohne eingebettete Zugangsdaten oder Fragment akzeptieren.
- Bearer-Token und Basic Auth schließen sich gegenseitig aus. Authentifizierung, Timeout und Größenlimit gelten für alle Endpunkte.
- Standardwerte: HTTP `15s`, Antwortlimit `1048576` Bytes; Broker `tcp://localhost:1883`, Client-ID `buderus-wp-to-mqtt`, Topic `buderus/wp`, QoS `0`, Retain `false`, MQTT-Timeout `5s`, Availability `true`.
- Nur Compose überschreibt für den optionalen Quarkus-Service Client-ID mit `buderus-wp-to-mqtt-quarkus` und Topic mit `buderus/wp-quarkus`. Beide Anwendungsstandards bleiben gleich; parallele Instanzen benötigen unterschiedliche IDs und Topic-Präfixe.
- `KeepAlive` ist eine interne MQTT-Konfiguration mit Standard `30s`, keine zusätzliche ENV-Variable. `AvailabilityTopic` ist ebenfalls Bestandteil der internen MQTT-Konfiguration.
- Standardmapping `fields`, Standardplan `@every 60s`, Startabfrage `true`, Zeitzone `UTC`, Log-Level `info`, Log-Format `json`.
- Dauerwerte müssen positiv sein; QoS ist `0` bis `2`. Log-Level: `debug/info/warn/error`; Log-Format: `json/text`.
- `API_MAX_BODY_BYTES` ist positiv. Go verwendet einen `int64`-Grenzwert; Quarkus begrenzt wegen Byte-Arrays auf `2147483646`. Diese Abweichung dokumentieren; für Vergleichsläufe gemeinsame gültige Werte und ausreichende RAM-Limits wählen.
- Keine versteckten Schalter, stillen Fallbacks bei ungültigen Werten oder unbeabsichtigten neuen ENV-Variablen hinzufügen.
- `.env.example` muss sowohl mit Compose `env_file` als auch mit Docker `--env-file` funktionieren. Dauer-/Cron-Werte mit Leerzeichen nicht quoten, da Docker Quotes wörtlich übernimmt. Die Datei ist kein Shell-Skript; Shell-Exports müssen separat quotiert werden.

## REST- und Mapping-Invarianten

- Die folgenden Daten- und Sicherheitsinvarianten gelten für beide Implementierungen; Go-spezifische Typen, Kontext- und Optionsschnittstellen gelten nur innerhalb von `go/`.
- In Go validieren `restclient.New` und Optionen Eingaben vor Netzwerkzugriffen. Optionen umfassen Timeout, maximale Body-Größe, Bearer, Basic Auth, Header, eigenen HTTP-Client und explizite unsichere TLS-Ausnahme.
- In Go schließt `Fetch(ctx)` Response-Bodies; Nicht-2xx liefert `*StatusError` mit Statuscode. Auch Quarkus muss HTTP-Ressourcen schließen und Statusfehler ohne Antwortdaten melden.
- Bewusster HTTP-Unterschied: Go folgt der Standard-Redirect-Policy seines `http.Client`; Quarkus verwendet `Redirect.NEVER` und behandelt `3xx` als Statusfehler. Direkte endgültige API-URLs dokumentieren und in fairen Vergleichsläufen Redirects vermeiden.
- Größenüberschreitung durch begrenztes Lesen mit einem zusätzlichen Prüfbyte erkennen. Niemals einen abgeschnittenen Body als erfolgreich behandeln.
- JSONFields akzeptiert genau ein JSON-Objekt, sortiert Schlüssel rekursiv und flacht verschachtelte Objekte zu Topic-Pfaden ab.
- Zahlenpräzision erhalten: in Go `json.Number` statt implizitem `float64`, in Java keine verlustbehaftete Umwandlung in `double`. Strings roh, Arrays als JSON, `null` als Text `null` übertragen.
- Für gültiges UTF-8-JSON Zahlenlexeme einschließlich Arrays erhalten und Schlüssel nach UTF-8-Bytefolge sortieren, nicht nach Java-UTF-16-Reihenfolge. Doppelte JSON-Objektschlüssel verwenden den letzten Wert. Parsergrenzen bleiben implementierungsabhängig: Jackson standardmäßig 1000, Go 10000 Verschachtelungsebenen; keine bytegleiche Behandlung fehlerhafter Unicode-Eingaben versprechen.
- `/`, `+`, `#`, Leerzeichen und NUL in Schlüsseln durch `_` ersetzen, leere Schlüssel durch `_`. Topic-Kollisionen müssen das Mapping als Ganzes ablehnen.
- Passthrough veröffentlicht unveränderte Antwortbytes. Nachrichten tragen ihr eigenes Retain-Flag.
- Einzelendpunkt: `MQTT_TOPIC[/feld]`; mehrere Endpunkte: `MQTT_TOPIC/name[/feld]`.
- `MQTT_TOPIC/status` ist bei aktiver Availability reserviert. Beide Anwendungen lehnen Mapping-Ergebnisse auf exakt diesem Topic vor der Veröffentlichung ab; auch der Publisher schützt das Status-Topic. In Go ist es als `MQTT.AvailabilityTopic` beziehungsweise `AvailabilityTopic` repräsentiert. Daten dürfen den Verfügbarkeitsstatus nicht überschreiben.

## Laufzeit und Betrieb

- Fünffeldrige Cron-Ausdrücke ohne Sekundenfeld und Standarddeskriptoren unterstützen; Intervalle müssen positiv sein.
- Überlappende Durchläufe überspringen, nicht parallel starten oder unbegrenzt nachholen. Startabfrage und Scheduler müssen denselben Ausschluss beachten.
- Go bettet IANA-Zeitzonendaten mit `time/tzdata` ein, damit das minimale Image keine externe Zonen-Datenbank benötigt. Auch der native Quarkus-Build muss die unterstützten IANA-Zonen zur Laufzeit bereitstellen.
- SIGINT/SIGTERM muss laufende Arbeit kontrolliert beenden und MQTT sauber schließen.
- Go verwendet `slog` auf stdout; beide Anwendungen liefern strukturiertes JSON standardmäßig, optional Text. Fehler mit Kontext protokollieren, ohne Geheimnisse preiszugeben.
- Quarkus-Startfehler beachten `LOG_FORMAT` und `LOG_LEVEL`, sobald diese Einstellungen erfolgreich validiert wurden; Fehler bei noch ungültiger Log-Konfiguration benötigen eine sichere Standardausgabe.
- Availability veröffentlicht retained `online` bei Verbindung und Reconnect; geordnetes Beenden und Last Will liefern retained `offline`.
- Für `online`, `offline` und Last Will gilt QoS `min(Daten-QoS, 1)`; Daten behalten die konfigurierte QoS `0/1/2`. Status-Replay muss eine vollständige PUBLISH-Meldung bleiben: Ein wiederholtes QoS-2-PUBREL kann den nach Last Will gespeicherten `offline`-Status nicht durch `online` ersetzen.
- Daten-Publish nur bei offener MQTT-Verbindung zulassen, nicht während Reconnect. Höchstens eine Datenmeldung darf gleichzeitig eingereiht beziehungsweise unbestätigt sein, einschließlich eines nach Kontextabbruch noch laufenden Enqueue-Vorgangs; weitere Publikationen bei ausstehender Zustellung mit `ErrPending` abweisen, keine unbegrenzte Offline-Warteschlange aufbauen.
- MQTT-Timeout und Kontextabbruch beenden nur das Warten, nicht zwingend die spätere Zustellung. Bei QoS 1/2 die Pending-Sperre erst nach tatsächlicher Broker-Bestätigung lösen; beim Go-Paho-Replay können Tokens ersetzt oder vorzeitig abgeschlossen werden, daher Persistenz-IDs berücksichtigen. QoS 0 besitzt keine Broker-Bestätigung; definitiv fehlgeschlagene oder verworfene Sendevorgänge dürfen die Sperre nicht dauerhaft halten.
- Quarkus wartet über eine unabhängige `CompletableFuture` auf den tatsächlichen Delivery-Callback, nicht über Pahos `token.waitForCompletion`: Ein unterbrochener Token-Warteaufruf verändert dessen Fehlerzustand. Timeout oder Thread-Unterbrechung dürfen die Pending-Sperre nicht lösen. Bei QoS 1/2 gibt erst die tatsächliche Broker-Bestätigung weitere Datenmeldungen frei; bei QoS 0 gibt es keine Broker-Bestätigung, und ein definitiv fehlgeschlagener beziehungsweise verworfener Sendevorgang muss die Sperre ebenfalls lösen. Insbesondere löst das nachgewiesene Verwerfen ungesendeter QoS-0-Meldungen durch Paho beim Disconnect deren Daten- und Statussperren; persistierte QoS-1/2-Meldungen bleiben bis zur Bestätigung gesperrt.
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
  cd go
  go build ./...
  go vet ./...
  go test ./... -race -cover
  golangci-lint run
  govulncheck ./...
  ```
- Quarkus separat vom Repository-Wurzelverzeichnis mit `mvn -f quarkus/pom.xml verify` prüfen. Der native Docker-Build ist eine eigene Prüfung: `docker build -t buderus-wp-to-mqtt-quarkus -f quarkus/Dockerfile quarkus`. JVM-Tests nicht als Nachweis nativer Ausführbarkeit ausgeben.
- Alternativer Native-Build: im Verzeichnis `quarkus/` mit JDK 21 `./mvnw package -Dnative -Dquarkus.native.container-build=true -Dquarkus.native.builder-image=quay.io/quarkus/ubi9-quarkus-mandrel-builder-image:jdk-25` ausführen, danach aus dem Repository-Wurzelverzeichnis mit `docker build -f quarkus/Dockerfile.native -t buderus-wp-to-mqtt-quarkus quarkus` das Laufzeitimage erstellen. `Dockerfile.native` benötigt ein vorhandenes natives Artefakt in `quarkus/target/`; es ersetzt nicht die native Kompilierung.
- `quarkus/src/test/container/smoke.py` unterstützt den gepackten JVM-Prozess mit `--jar quarkus/target/quarkus-app/quarkus-run.jar` und den nativen Container mit `--image IMAGE`, jeweils zusätzlich `--tls` für lokale HTTPS-/MQTT-TLS-Gegenstellen. Beide Laufzeitformen getrennt validieren; erfolgreiche JVM-Smokes ersetzen keine nativen Smokes.
- `gofmt` und `goimports` für Go-Code verwenden. `go mod tidy` bei Abhängigkeitsänderungen; `go.mod` und `go.sum` gemeinsam prüfen.
- Keine Abhängigkeiten installieren, solange weder Manifeständerung noch ein fehlendes Werkzeug die Installation erfordert.
- Dokumentationsänderungen allein benötigen keine Tests oder Builds. Nicht ausgeführte Prüfungen und tatsächliche Einschränkungen ehrlich benennen.

## Docker und Deployment

- Go: statischer Build über `go/Dockerfile`, Laufzeit auf `scratch`, CA-Zertifikate verfügbar, Non-root UID/GID `65534`. Keine Shell oder Healthcheck-Binärdatei im Image voraussetzen.
- Compose-Service `buderus-wp-to-mqtt`: Image `ghcr.io/patrickhofmann0/buderus-wp-to-mqtt:latest`, `build: ./go`, `env_file: .env`, `restart: unless-stopped`, 32 MB RAM, 0,1 CPU, schreibgeschütztes Dateisystem.
- Quarkus: nativer Multistage-Build mit Mandrel **25** (`quay.io/quarkus/ubi9-quarkus-mandrel-builder-image:jdk-25`), Java-Quellcode-Release **21** und eigenem Build-Kontext `quarkus/`. Laufzeit auf `quay.io/quarkus/ubi9-quarkus-micro-image:2.0`, Non-root UID/GID `65534:65534`, ohne JVM. Beide Dockerfiles verwenden dafür `COPY --chown=65534:65534` und `USER 65534:65534`; der Mandrel-Builder behält seinen eigenen Benutzer. Builder und Laufzeit müssen UBI-9-glibc-kompatibel bleiben; nicht mit dem älteren Micro-Image ohne `ubi9` austauschen. Keine Go-Dateien dafür voraussetzen. Native Kompilierung benötigt mehr Ressourcen als die Laufzeit; keine 32-MB-Zusage ableiten.
- Optionaler Compose-Service `buderus-wp-to-mqtt-quarkus` nur im Profil `quarkus`: Image `ghcr.io/patrickhofmann0/buderus-wp-to-mqtt-quarkus:latest`, `build: ./quarkus`, gemeinsame `.env`, schreibgeschütztes Dateisystem, zunächst 128 MB RAM und 0,1 CPU. Flüchtiges `/tmp` nur als begrenztes `tmpfs`: `size=16m,uid=65534,gid=65534,mode=1777`; dessen Speicher zählt zum RAM-Limit.
- Quarkus-Image-Veröffentlichung zunächst nur für `linux/amd64`; Go bleibt multiarch. ARM erfordert einen nativen Quarkus-Neubau auf einem entsprechenden Build-Host. Keine unbelegte ARM-Image-Verfügbarkeit oder architekturübergreifende Vergleichbarkeit versprechen.
- Optionale Mosquitto-Instanz nur im Profil `dev`; `deploy/mosquitto.conf` einbinden, Host-Port 1883 nur an Loopback veröffentlichen. Anonymer Zugriff ist ausschließlich für lokale Entwicklung.
- Im Container bezeichnet `localhost` nicht den Host. Der Dev-Broker wird mit `MQTT_BROKER=tcp://mosquitto:1883` angesprochen.
- Größen- und RAM-Angaben sind Schätzungen, keine Zusagen. Architektur, Versionen, TLS und große Antworten berücksichtigen.
- Kein Benchmark-Harness und keine gemessene Überlegenheit behaupten. Für faire Ressourcen-/Startzeitvergleiche nur eine Variante gleichzeitig mit identischen Eingaben, MQTT-Einstellungen und angeglichenen Limits betreiben; Image-Größe, Startzeit und Laufzeitressourcen getrennt erfassen. Parallelvergleiche benötigen getrennte Client-IDs und Topic-Präfixe.

## Änderungen, CI und Reviews

- Die bestehende CI unter `.github/workflows/ci.yml` und vorhandene Release-/Containerabläufe respektieren.
- Go-Jobs verwenden `go/` als Arbeitsverzeichnis und `go/` als Docker-Build-Kontext; Quarkus-Jobs prüfen das separate Maven-Projekt und bauen aus `quarkus/`. Die bestehenden Go-Prüfungen nicht durch reine Java-Prüfungen ersetzen.
- Quarkus-CI nutzt Java 21 für `mvn verify`, prüft danach den gepackten JVM-Prozess mit `smoke.py --jar` ohne und mit `--tls`, baut und lädt das native Image als `buderus-wp-to-mqtt-quarkus:ci` und prüft es mit `smoke.py --image` ebenfalls ohne und mit `--tls`. Image-Smokes dürfen keine lokale Native-Toolchain oder ein Host-`JAVA_HOME` voraussetzen. Ubuntu-Runner stellen Python 3, OpenSSL und Docker bereit. GHCR-Veröffentlichung des Quarkus-Images zunächst für `linux/amd64`, unabhängig vom bestehenden Go-Multiarch-Build.
- Prüfwerkzeuge mit derselben Go-Toolchain wie den Service bauen. Die CI installiert golangci-lint aus Quellcode und nutzt nach `setup-go` `GOTOOLCHAIN=local`, damit ein älter gebautes Werkzeug die aktuelle Go-Version nicht ablehnt.
- Neue Konfiguration, Schnittstellen oder geändertes Fehlerverhalten mit passenden Tests und aktualisierter Dokumentation liefern.
- Keine Geheimnisse oder generierten Binärdateien einchecken. Diffs vor dem Commit auf unbeabsichtigte Änderungen prüfen.
- PR-Beschreibungen nennen Zweck, relevante Verhaltensänderungen, ausgeführte Prüfungen und verbleibende Einschränkungen.
- Fachliche und architektonische Entscheidungen sind erst vollständig umgesetzt, wenn **diese Datei im selben Commit/PR aktualisiert** wurde; Konfigurationsänderungen benötigen zudem README und ENV-Beispiel.
