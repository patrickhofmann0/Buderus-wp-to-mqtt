# Entwicklungs-Image: Go 1.27, golangci-lint und opencode in festen Versionen.
FROM mcr.microsoft.com/devcontainers/go:1.27

# Gleiche Version wie in der CI (.github/workflows/build.yml).
ARG GOLANGCI_LINT_VERSION=v2.15.0
# opencode V2; zum Aktualisieren hier die Version erhöhen und Container neu bauen.
ARG OPENCODE_VERSION=2.0.19

RUN apt-get update \
    && apt-get install -y --no-install-recommends make jq mosquitto-clients \
    && rm -rf /var/lib/apt/lists/*

RUN curl -sSfL https://golangci-lint.run/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_LINT_VERSION}"

USER vscode

RUN curl -fsSL https://opencode.ai/v2/install \
    | bash -s -- --version "${OPENCODE_VERSION}" --no-modify-path

# Verzeichnisse vorab als vscode anlegen: Named Volumes übernehmen beim ersten
# Start Inhalt und Besitzer aus dem Image, Bind-Mounts legen sonst root-eigene
# Elternverzeichnisse an.
RUN mkdir -p /home/vscode/.local/share/opencode /home/vscode/.config/opencode

USER root

# Symlink statt ENV PATH: Login-Shells (wie VS Code sie nutzt) setzen den PATH
# über /etc/profile zurück.
RUN ln -s /home/vscode/.opencode/bin/opencode /usr/local/bin/opencode
