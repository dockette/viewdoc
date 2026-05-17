<h1 align=center>Dockette / Viewdoc</h1>

<p align=center>
    📄 🎞️ 🖼️ Browser-embeddable remote viewer for PDFs, office docs, audio, video, and images.
    A small Go control-center fronts a fixed pool of <a href="https://www.kasmweb.com/">Kasm</a> /
    <a href="https://docs.linuxserver.io/images/docker-webtop/">LinuxServer Webtop</a> desktop containers —
    pass <code>?url=...</code> and the file opens in the right app inside an iframe.
</p>

<p align=center>
🕹 <a href="https://f3l1x.io">f3l1x.io</a> | 💻 <a href="https://github.com/f3l1x">f3l1x</a> | 🐦 <a href="https://twitter.com/xf3l1x">@xf3l1x</a>
</p>

<p align=center>
  <a href="https://hub.docker.com/r/dockette/viewdoc/"><img src="https://badgen.net/docker/pulls/dockette/viewdoc"></a>
  <a href="https://bit.ly/ctteg"><img src="https://badgen.net/badge/support/gitter/cyan"></a>
  <a href="https://github.com/sponsors/f3l1x"><img src="https://badgen.net/badge/sponsor/donations/F96854"></a>
</p>

-----

## Why

Embedding rich file viewers in your product is a thousand papercuts: PDF.js for some types, an office viewer for others, a media player for the rest — each with its own quirks, sandbox story, and asset list. **Viewdoc skips it.** Open a real desktop in a tab, let the *desktop* open the file with its native app, and stream the pixels back. One iframe, every file type.

```
browser ── http://localhost:8080/?url=… ──► control-center (Go)
                                              ├── reverse-proxy ──► viewer-*:6901  (KasmVNC)
                                              └── HTTP POST ─────► viewer-*:7000   (sidecar)
                                                                       └── viewdoc.sh → firefox / vlc / xdg-open
```

## Quickstart

```sh
docker compose build
docker compose up -d

open http://localhost:8080/                                   # iframe + URL bar + slot tabs
open "http://localhost:8080/?url=https://example.com/x.pdf"   # auto-opens in slot 0
curl -s http://localhost:8080/healthz                         # {"ready":4,"total":4}
```

Default pool (`VIEWER_SLOTS`): 2× Kasm + 2× Webtop.

## Endpoints

| Method | Path                         | Purpose                              |
|--------|------------------------------|--------------------------------------|
| GET    | `/`                          | UI: iframe + URL bar + slot tabs     |
| GET    | `/healthz`                   | `{ready, total}`                     |
| GET    | `/api/slots`                 | slot table with reachability         |
| POST   | `/api/slot/{i}/params`       | forward `{params:{url,…}}` to slot   |
| ANY    | `/slot/{i}/*`                | reverse-proxy → `<slot[i]>:port`     |

## Environment

| Var             | Default                                      | Purpose                                                  |
|-----------------|----------------------------------------------|----------------------------------------------------------|
| `LISTEN_ADDR`   | `:8080`                                      | control-center bind                                       |
| `VIEWER_SLOTS`  | `viewer-1,viewer-2,viewer-3`                 | comma-separated `host[:port]`; port defaults to `6901`   |

- **Kasm slots** — prefix `https://` and use `:6901` (KasmVNC serves self-signed HTTPS; the proxy skips cert verification).
- **Webtop slots** — omit scheme, specify `:3000` (plain HTTP).

-----

Consider to [support](https://github.com/sponsors/f3l1x) **f3l1x**. Also thank you for using this package.
