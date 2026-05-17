FROM golang:1.23-alpine AS sidecar
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd/sidecar ./cmd/sidecar
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/sidecar ./cmd/sidecar

FROM kasmweb/ubuntu-noble-desktop:1.18.0
USER root

RUN apt-get update \
 && apt-get install -y --no-install-recommends vlc xdg-utils libreoffice \
 && rm -rf /var/lib/apt/lists/*

# KasmVNC validates pem paths at boot even when require_ssl is false; the
# entrypoint auto-generates self.pem on first run.
COPY .docker/kasm/kasm.yml /etc/kasmvnc/kasmvnc.yaml

COPY --from=sidecar /out/sidecar /usr/local/bin/viewdoc-sidecar
COPY .docker/viewdoc.sh /usr/local/bin/viewdoc.sh
RUN chmod 0755 /usr/local/bin/viewdoc-sidecar /usr/local/bin/viewdoc.sh

# Autostart the sidecar via Kasm's custom-startup hook. Also patch firefox's
# profiles.ini: Kasm ships a stale [Install...] token from an older firefox
# build with Locked=1, so firefox 144 fails to match it and surfaces
# "Profile Missing" instead of falling back. Rewriting to a plain Default=1
# lets firefox lazily populate the existing `kasm` profile dir on first run.
#
# Kasm's vnc_startup.sh monitors custom_startup.sh's PID and prints
# "Unknown Service: custom_startup" if it exits — so the script must stay
# alive. Exec the sidecar so it becomes the tracked long-running process.
RUN cat > /dockerstartup/custom_startup.sh <<'EOF' \
 && chmod 0755 /dockerstartup/custom_startup.sh
#!/usr/bin/env bash
PROFILES_INI=/home/kasm-user/.mozilla/firefox/profiles.ini
if [ -f "$PROFILES_INI" ] && grep -q '^Locked=1' "$PROFILES_INI"; then
  cat > "$PROFILES_INI" <<'INI'
[Profile0]
Name=default
IsRelative=1
Path=kasm
Default=1

[General]
StartWithLastProfile=1
Version=2
INI
fi
exec /usr/local/bin/viewdoc-sidecar
EOF

USER 1000
EXPOSE 6901 7000
