FROM golang:1.23-alpine AS sidecar
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd/sidecar ./cmd/sidecar
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/sidecar ./cmd/sidecar

FROM lscr.io/linuxserver/webtop:ubuntu-xfce

RUN apt-get update \
 && apt-get install -y --no-install-recommends vlc xdg-utils ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY --from=sidecar /out/sidecar /usr/local/bin/viewdoc-sidecar
COPY .docker/viewdoc.sh /usr/local/bin/viewdoc.sh
RUN chmod 0755 /usr/local/bin/viewdoc-sidecar /usr/local/bin/viewdoc.sh

# Selkies defaults to 96 DPI + auto-resize-to-iframe, which renders xfce
# uncomfortably small. Lock the desktop to 1920x1080 in manual-resolution
# mode and bump DPI to 144 (≈1.5x, GTK-aware so it stays sharp). The LSIO
# image hard-codes the selkies args in its s6 run script, so patch it.
RUN sed -i 's|--mode="websockets"|--mode="websockets" --scaling-dpi="144" --is-manual-resolution-mode="true" --manual-width="1920" --manual-height="1080"|' \
        /etc/s6-overlay/s6-rc.d/svc-selkies/run

RUN set -eux; \
    mkdir -p /etc/s6-overlay/s6-rc.d/viewdoc-sidecar/dependencies.d \
             /etc/s6-overlay/s6-rc.d/user/contents.d; \
    echo "longrun" > /etc/s6-overlay/s6-rc.d/viewdoc-sidecar/type; \
    # s6-setuidgid drops to abc so VLC will actually run (it refuses root)
    # and so launched apps inherit the same user as the xfce session — same
    # X authority, pulse socket, chromium profile under /tmp/viewdoc-chromium.
    # with-contenv pulls HOME=/config, DISPLAY=:1, XDG_RUNTIME_DIR=/config/.XDG,
    # PULSE_RUNTIME_PATH=/defaults from the container env.
    printf '#!/usr/bin/with-contenv bash\nexec s6-setuidgid abc /usr/local/bin/viewdoc-sidecar\n' \
      > /etc/s6-overlay/s6-rc.d/viewdoc-sidecar/run; \
    chmod 0755 /etc/s6-overlay/s6-rc.d/viewdoc-sidecar/run; \
    # Depend on svc-pulseaudio so the sidecar starts only after the abc-user's
    # pulse daemon is up — VLC's first dispatch would otherwise race the
    # /defaults/native socket coming online.
    touch /etc/s6-overlay/s6-rc.d/viewdoc-sidecar/dependencies.d/svc-pulseaudio; \
    touch /etc/s6-overlay/s6-rc.d/user/contents.d/viewdoc-sidecar

EXPOSE 3000 6901 7000
