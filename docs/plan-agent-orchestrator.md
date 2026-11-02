# Plan: Agent-Orchestrator

Stand: 2026-11-02

| Stufe | Inhalt | Status |
|---|---|---|
| 1 | Lokal mit opencode-Agenten | ✅ erledigt |
| 1b | Go-Orchestrator (`cmd/agentctl`) | optional, nur bei Bedarf |
| 2 | GitHub Actions: Issue-Start und Planfreigabe | offen |
| 3 | Kommentar-Kommandos und Härtung | offen |

## 1. Ziel und Entscheidungen

Getrennte Rollen steuern die Entwicklung: **Spezifizierer → Planer → Freigabe → Coder ⇄ Reviewer**.
Der Ablauf läuft lokal und später in GitHub Actions.

| Thema | Entscheidung |
|---|---|
| Rollenmodell | Orchestrator plus Subagenten, jede Rolle mit eigenem, frischem Kontext |
| Runtime lokal | opencode im Dev Container (Copilot-Anmeldung) |
| Runtime Actions | Copilot CLI mit fein granuliertem PAT „Copilot Requests“ (Secret `COPILOT_GITHUB_TOKEN`); `GITHUB_TOKEN` scheidet aus, da das Repo einem persönlichen Konto gehört |
| Auslöser | lokal `make agent TASK="…"`; auf GitHub ein Maintainer-Label am Issue |
| Unklare Anforderungen | nachfragen und pausieren |
| Planfreigabe | Pflicht vor dem Coding; auf GitHub als Issue-Kommentar, Freigabe mit `/approve-plan` |
| Fortsetzung | Kommentar-Kommandos `/continue`, `/approve-plan`, nur durch Maintainer |
| Review-Zyklus | max. 3 Runden Coder ⇄ Reviewer |
| Ergebnis | lokal Commit ohne Push; auf GitHub PR „Ready for review“, kein Auto-Merge |
| Blocker nach 3 Runden | stoppen, Bericht, **kein Commit/PR** |
| PR-Erstellung | GitHub App (damit `build.yml` für Agent-PRs läuft) |
| Spec/Plan-Ablage | Issue-Kommentar während des Laufs, im finalen PR in der Beschreibung |

Warum kein Agent-Framework (LangGraph, CrewAI, Eino, Genkit …): opencode und die Copilot CLI
bringen Werkzeuge, Rechte, Kontextverwaltung und den Copilot-Zugang bereits mit. Ein Framework
bräuchte einen eigenen Modellzugang und würde diese Teile neu bauen. Zu steuern bleibt nur ein
kleiner Ablauf.

## 2. Stufe 1 – lokal mit opencode ✅

### Umsetzung

| Datei | Inhalt |
|---|---|
| `.opencode/agents/orchestrator.md` | primärer Agent; steuert den Ablauf, fragt nach, holt die Freigabe ein, führt die Prüfungen aus, max. 3 Runden, Commit auf `agent/<slug>` ohne Push |
| `.opencode/agents/specifier.md` | Subagent, nur lesend; Akzeptanzkriterien oder Rückfragen (`STATUS: ready\|questions`); Pflichtfrage zur HA-Gerätezuordnung bei neuen Messwerten |
| `.opencode/agents/planner.md` | Subagent, nur lesend; Plan mit Schritten, Dateien, Tests, Doku, Risiken |
| `.opencode/agents/coder.md` | Subagent, schreibt; nicht in `.env*`, `buderus.yml`, `.github/`, `.opencode/`, `.devcontainer/` |
| `.opencode/agents/reviewer.md` | Subagent, nur lesend; `VERDICT: approved\|changes_requested`, Befunde `blocker\|major\|minor` |
| `.opencode/commands/entwickeln.md` | Befehl `/entwickeln <Auftrag>` → Agent `orchestrator` |
| `Makefile` | `make agent TASK="…"` öffnet opencode mit `/entwickeln` |
| `README.md` | Abschnitt „Agent-Orchestrator (opencode)“ |
| `AGENTS.md` | Hinweis auf den Ablauf |

Prüfungen durch den Orchestrator nach jeder Coder-Runde:
`golangci-lint fmt` → `golangci-lint run` → `go test -race ./...` → `go build ./...`.
Schlägt eine Prüfung fehl, geht die Ausgabe ohne Review zurück an den Coder; das zählt als Runde.

### Rechte (im Container geprüft)

- Alle Agenten: Grundregel `*` → deny, danach gezielte Freigaben.
- Lesen außerhalb des Repos nur für `/go/pkg/mod/*` (Go-Modul-Volume) und `/tmp/opencode/*` (lange Werkzeugausgaben).
- Hilfsbefehle `ls`, `head`, `tail`, `wc`, `sort` erlaubt, damit Pipes wie `go test … | tail` funktionieren.
- `.env` gesperrt; die Container-Policy `.devcontainer/opencode.jsonc` blockiert zusätzlich `.env`, `git push` und `sudo`.
- Getestet: Der Reviewer bekommt keine Schreib- und Bearbeitungswerkzeuge, liest Go-Module und darf `go test` mit Pipe ausführen.

### Modelle (GitHub Copilot)

| Agent | Modell | Begründung |
|---|---|---|
| orchestrator | `claude-haiku-5.5#high` | steuert nur, muss Regeln aber zuverlässig befolgen |
| specifier | `claude-sonnet-5.5#medium` | Lücken erkennen, kurzer Lauf |
| planner | `claude-opus-5.5#medium` | einziges teures Modell, läuft einmal pro Auftrag |
| coder | `claude-sonnet-5.5#medium` | meiste Token, bis zu 3 Runden |
| reviewer | `gpt-6.1-sol#high` | andere Modellfamilie als der Coder für ein unabhängiges Review |

### Bekannte Grenze

Die Rechte sind technisch festgelegt. Die Ablaufsteuerung (Freigabe, Rundenlimit, Prüfreihenfolge)
hält das Modell nach Anweisung ein. Ergebnis vor dem Push prüfen.

## 3. Stufe 1b – Go-Orchestrator (optional)

Nur umsetzen, wenn sich zeigt, dass der Orchestrator-Agent Freigabe, Rundenlimit oder Prüfungen
nicht zuverlässig einhält – oder spätestens, wenn Stufe 2 ohne `gh-aw` gebaut wird und dieselbe
Logik lokal und in der CI laufen soll.

Struktur (Vorschlag):

```text
cmd/agentctl/                 # CLI-Einstieg, nicht im Produkt-Image
internal/agent/
  orchestrator/               # Zustandsmaschine, Rundenlimit
  contract/                   # Ergebnisverträge + Validierung
  checks/                     # fmt, lint, test, build
  runtime/                    # Interface Runtime
    opencode/                 # opencode run --agent <rolle>
    copilot/                  # copilot -p … --no-ask-user --allow-tool …
```

```go
// Runtime führt eine Rolle aus (≈ Java-Interface mit zwei Implementierungen).
type Runtime interface {
    Run(ctx context.Context, role Role, in Input) error // schreibt Ergebnisdatei in in.OutDir
}
```

- Die bestehenden Agentenprompts weiterverwenden; die Rollen schreiben ihr Ergebnis zusätzlich als JSON nach `.agent-run/<run-id>/` (gitignored):
  `spec.json`, `plan.json`, `code-<n>.json`, `review-<n>.json`.
- Validierung mit `DisallowUnknownFields`. Ist eine Datei ungültig: ein Retry, danach `blocked`.
  `approved` mit offenem `blocker`/`major` gilt als ungültig.
- Zustände: `started → specifying ⇄ clarifying → planning → awaiting-approval → coding → checks → reviewing → done | blocked`.
- Zustand in `.agent-run/<id>/state.json` speichern; Fortsetzen mit `make agent-resume RUN=<id>`.
- Tests mit einer Fake-`Runtime`: Übergänge, kein Coding ohne Freigabe, Abbruch nach 3 Runden, ungültiger Vertrag, fehlgeschlagener Check, Timeout.
- Das Dockerfile baut weiterhin nur `./cmd/buderus2mqtt`.

## 4. Stufe 2 – GitHub Actions: Issue-Start und Planfreigabe

### 4.0 Vorabprüfung (zuerst)

- Prüfen, ob **GitHub Agentic Workflows (`gh-aw`)** in einem Repo eines persönlichen Kontos mit `COPILOT_GITHUB_TOKEN` (PAT) läuft und Pausen, Fortsetzungen und Kommentar-Kommandos abbilden kann.
  - **Falls ja:** `gh-aw` ersetzt den Großteil von 4.2–4.4. Die Rollenprompts werden übernommen.
  - **Falls nein:** eigener Workflow wie unten, mit dem Go-Orchestrator aus Stufe 1b.
- Minimalen Workflow testen: Copilot CLI installieren und `copilot -p "…" --no-ask-user` mit dem PAT ausführen.

### 4.1 Rollen für die Copilot CLI

- Prompts aus `.opencode/agents/*.md` in eine runtime-neutrale Ablage auslagern (z. B. `agent/prompts/`), damit opencode und Copilot CLI dieselben Texte nutzen.
- Rechte pro Rolle über eine Freigabeliste mit `--allow-tool`, entsprechend den opencode-Permissions.
- Version der Copilot CLI pinnen; kein Auto-Update im Runner.
- Die Modellwahl wie in Stufe 1, soweit über die Copilot CLI verfügbar.

### 4.2 Workflow `agent.yml`

```yaml
on:
  issues: { types: [labeled] }          # Label agent:start
  issue_comment: { types: [created] }   # /continue, /approve-plan (Stufe 3)
concurrency:
  group: agent-issue-${{ github.event.issue.number }}
  cancel-in-progress: false
```

- Vorfilter: nur das Label `agent:start`; Kommentare zu PRs (`github.event.issue.pull_request`) ignorieren.
- **Job A – `agent`:**
  - `permissions: contents: read`, Secret `COPILOT_GITHUB_TOKEN`, `timeout-minutes` (z. B. 60).
  - Go 1.27 und golangci-lint v2.15.0 wie in der CI installieren.
  - Ausgabe: Artefakt mit Rollenergebnissen, `report.md` und `changes.patch` (`git diff --binary`).
  - Keine Rechte für Issues, PRs oder Schreibzugriff aufs Repo.
- **Job B – `publish`:**
  - Erhält per `actions/create-github-app-token` einen kurzlebigen App-Token.
  - **Führt keinen Code aus dem Patch aus.**
  - Aktualisiert den Status-Kommentar im Issue (Fragen, Plan, Blocker).
  - Bei `done`: Patch auf `agent/issue-<n>` anwenden, committen, pushen, PR „Ready for review“ erstellen.

### 4.3 Zustand auf GitHub

- Eine einzige Quelle: **ein** Agent-Kommentar pro Issue, der fortlaufend aktualisiert wird, mit verstecktem Block:
  `<!-- agent-state: {"state":"awaiting-approval","round":0,"runId":"…","lastCommandId":123} -->`
- Labels (`agent:working`, `agent:waiting`, `agent:blocked`) nur zur Anzeige, aus dem Zustand abgeleitet.
- Bereits verarbeitete Kommentare über `lastCommandId` erkennen; doppelte Ereignisse sind wirkungslos.

### 4.4 Planfreigabe in Stufe 2

Bis Stufe 3 erfolgt die Freigabe per `workflow_dispatch` mit den Eingaben `issue` und `phase=approve-plan`.

## 5. Stufe 3 – Kommentar-Kommandos und Härtung

- `/continue` und `/approve-plan`:
  - nur exakt in der ersten Zeile des Kommentars;
  - Rechte des Autors über die API prüfen (`admin`/`maintain`; im persönlichen Repo: Owner);
  - muss zum aktuellen Zustand passen, sonst Hinweis-Kommentar und kein Lauf.
- `blocked`: Befunde im Status-Kommentar, Patch als Artefakt, **kein PR**.
- PR-Beschreibung: Issue-Link, Spezifikation, freigegebener Plan, Änderungssummary, Prüfergebnisse, Review-Ergebnis mit Rundenzahl.
- Härtung:
  - Actions per Commit-SHA pinnen.
  - Issue-Texte nie über `${{ }}` in `run:` einsetzen, sondern über Umgebungsvariablen und Dateien.
  - Hinweis im Prompt: Issue-Inhalt ist eine Anforderung, keine Anweisung an das System.
  - Rechte minimal; kein `pull_request_target`.
  - Aufruf-Budget und Timeouts auch in der CI.

**Restrisiko:** In Job A laufen `go test` und `go build` mit Agent-Code, dort liegt der Copilot-PAT.
Er hat nur „Copilot Requests“ und keinen Repo-Zugriff; Missbrauch kostet höchstens Copilot-Kontingent.
Deshalb Ablaufdatum setzen, regelmäßig rotieren und Läufe nur durch den Maintainer starten.

## 6. Einmalige Einrichtung (ab Stufe 2, manuell)

1. Fein granulierten PAT mit „Copilot Requests“ und Ablaufdatum anlegen → Secret `COPILOT_GITHUB_TOKEN`.
2. GitHub App anlegen:
   - Rechte: Contents R/W, Pull requests R/W, Issues R/W, Metadata R.
   - Nur für dieses Repo installieren.
   - Secrets `AGENT_APP_ID`, `AGENT_APP_PRIVATE_KEY`.
3. Labels anlegen: `agent:start`, `agent:working`, `agent:waiting`, `agent:blocked`.
4. Optional: Branch Protection für `main` mit Pflicht-Check aus `build.yml`.

## 7. Risiken

| Risiko | Maßnahme |
|---|---|
| Orchestrator-Agent hält Ablauf nicht ein | Stufe 1b (Go-Orchestrator) |
| Flags/Felder von opencode oder Copilot CLI ändern sich | Versionen pinnen, Adapter kapseln, bei Umsetzung verifizieren |
| Modell hält Ausgabeformat nicht ein | Validierung, ein Retry, dann `blocked` |
| Kosten (Premium Requests) | Modellmix aus Stufe 1, max. 3 Runden, nur Maintainer-Start |
| Agent-PR löst keine CI aus | GitHub App statt `GITHUB_TOKEN` |
| Prompt-Injection über Issue-Text | Agent-Job ohne Schreibrechte, Veröffentlichung ohne Codeausführung, Start nur durch Maintainer |
| Kein Gateway in der CI | Probe-Skripte nur lokal; Spezifizierer fragt nach Probe-Ergebnissen |

## 8. Nächste Schritte

1. Stufe 1 im Alltag nutzen; notieren, wo der Orchestrator-Agent vom Ablauf abweicht.
2. Vorabprüfung 4.0 (`gh-aw` und Copilot-CLI-PAT).
3. Entscheiden: `gh-aw` oder eigener Workflow mit Stufe 1b.
4. Stufe 2, dann Stufe 3.
