#!/usr/bin/env bash
# Fired by the sidecar after writing /tmp/viewdoc.{json,env}.
# Dedupes via $LAST so repeated POSTs from the iframe don't re-launch the app.
# All dispatch lines AND each launched app's stdout/stderr go to ONE place:
# /tmp/viewdoc.log + the sidecar's stderr. So `docker logs <viewer>` and
# `tail -f /tmp/viewdoc.log` show the exact same stream.

set -eu

QUERY_ENV="/tmp/viewdoc.env"
LAST="/tmp/.viewdoc_last_url"
LOG="/tmp/viewdoc.log"

log() {
  # Write to file + stderr separately (not via `tee`) so set -e doesn't bail
  # the script if either sink fails; logging must never be load-bearing.
  local msg
  msg="[viewdoc $(date -u +%H:%M:%S)] $*"
  printf '%s\n' "$msg" >> "$LOG" 2>/dev/null || true
  printf '%s\n' "$msg" >&2 || true
}

[ -r "$QUERY_ENV" ] || { log "no $QUERY_ENV; nothing to do"; exit 0; }
# shellcheck disable=SC1090
. "$QUERY_ENV"

url="${VIEWDOC_Q_URL:-}"
if [ -z "$url" ]; then
  log "VIEWDOC_Q_URL empty; skipping"
  exit 0
fi

if [ -r "$LAST" ] && [ "$(cat "$LAST")" = "$url" ]; then
  log "dedup hit, $url already open"
  exit 0
fi
printf '%s' "$url" > "$LAST"

lower="$(printf '%s' "$url" | tr '[:upper:]' '[:lower:]')"
path="${lower%%\?*}"; path="${path%%#*}"
ext="${path##*.}"

run() {
  log "exec: $*"
  # tee fans the launched app's combined stdout/stderr to both the single
  # log file and the sidecar's stderr — keeps the file as the durable
  # record AND surfaces output in `docker logs <viewer>` as it happens.
  # setsid detaches the app from this shell so the sidecar returns promptly.
  ( setsid "$@" </dev/null 2>&1 | tee -a "$LOG" >&2 ) &
}

case "$ext" in
  mp4|mkv|webm|mov|avi|mpeg|mpg|m4v|3gp|3g2|ts|mts|m2ts|vob|wmv|asf|divx|ogv|mp3|wav|flac|ogg|m4a|aac|opus|mka|wma|aiff|ape|mid)
    log "dispatch=vlc ext=$ext url=$url"
    # --no-qt-privacy-ask suppresses VLC's first-run "Privacy and Network
    # Access Policy" modal, which otherwise blocks playback until clicked.
    run vlc --no-qt-privacy-ask --no-video-title-show "$url"
    ;;
  doc|docx|odt|rtf|xls|xlsx|ods|csv|ppt|pptx|odp)
    log "dispatch=soffice ext=$ext url=$url"
    # soffice handles http(s) URLs natively via UCB. --view opens read-only,
    # --norestore kills the "recover document?" prompt after a previous
    # session, --nologo suppresses the splash.
    run soffice --view --norestore --nologo "$url"
    ;;
  zip|7z|rar|tar|gz|tgz|bz2|tbz2|xz|txz)
    # Archive managers want a local file, not a URL — download first, then
    # open. curl runs synchronously here because the sidecar already forked
    # viewdoc.sh as a separate process, so blocking here doesn't delay the
    # sidecar's HTTP response.
    tmpfile="/tmp/viewdoc-archive.$ext"
    log "dispatch=xarchiver ext=$ext url=$url -> $tmpfile"
    if curl -fsSL --max-time 60 -o "$tmpfile" "$url"; then
      run xarchiver "$tmpfile"
    else
      log "download failed url=$url"
    fi
    ;;
  pcap|pcapng|cap)
    tmpfile="/tmp/viewdoc-capture.$ext"
    log "dispatch=wireshark ext=$ext url=$url -> $tmpfile"
    if curl -fsSL --max-time 60 -o "$tmpfile" "$url"; then
      run wireshark -r "$tmpfile"
    else
      log "download failed url=$url"
    fi
    ;;
  eml)
    # claws-mail opens .eml files in a message-view window. Without a
    # pre-seeded account its first-run wizard insists on account setup
    # before the viewer is reachable, so write a stub local-mbox account
    # to an isolated config dir — enough for account_get_list() to be
    # non-empty, which is the condition that suppresses the wizard.
    tmpfile="/tmp/viewdoc-mail.eml"
    cfgdir="/tmp/viewdoc-claws"
    if [ ! -f "$cfgdir/accountrc" ]; then
      mkdir -p "$cfgdir"
      printf '<?xml version="1.0" encoding="UTF-8"?>\n<folderlist>\n</folderlist>\n' \
        > "$cfgdir/folderlist.xml"
      cat > "$cfgdir/accountrc" <<'CFG'
[Account: 1]
account_name=local
name=Viewdoc
address=viewdoc@example.invalid
protocol=2
recv_at_getall=0
CFG
    fi
    log "dispatch=claws-mail ext=$ext url=$url -> $tmpfile"
    if curl -fsSL --max-time 60 -o "$tmpfile" "$url"; then
      run claws-mail --alternate-config-dir "$cfgdir" "$tmpfile"
    else
      log "download failed url=$url"
    fi
    ;;
  *)
    # Chromium with --no-sandbox: Kasm's firefox 144 trips over a stale
    # profiles.ini Locked=1, and user namespaces are blocked in the container.
    # --user-data-dir isolates from any chromium the desktop autostarts
    # (webtop ships one wired to /config/.config/chromium, whose
    # SingletonLock embeds the previous container's hostname — recreating
    # the container leaves a stale lock that wedges every new launch).
    if printf '%s' "$url" | grep -qE '^https?://'; then
      for b in chromium chromium-browser google-chrome chrome; do
        if command -v "$b" >/dev/null 2>&1; then
          log "dispatch=$b ext=$ext url=$url"
          run "$b" --no-sandbox --user-data-dir=/tmp/viewdoc-chromium --new-window "$url"
          exit 0
        fi
      done
    fi
    log "dispatch=xdg-open ext=$ext url=$url"
    run xdg-open "$url"
    ;;
esac
