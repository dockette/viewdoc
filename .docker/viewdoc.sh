#!/usr/bin/env bash
# Fired by the sidecar after writing /tmp/viewdoc.{json,env}.
# Dedupes via $LAST + $STATE so repeated POSTs from the iframe don't re-launch
# an app that is still showing the same URL (see is_open).
# All dispatch lines AND each launched app's stdout/stderr go to ONE place:
# /tmp/viewdoc.log + the sidecar's stderr. So `docker logs <viewer>` and
# `tail -f /tmp/viewdoc.log` show the exact same stream.

set -eu

QUERY_ENV="/tmp/viewdoc.env"
LAST="/tmp/.viewdoc_last_url"
STATE="/tmp/.viewdoc_last_state"
LOG="/tmp/viewdoc.log"
# Latest outcome for the sidecar's GET /status (key=value lines).
STATUS="/tmp/viewdoc.status"
# A launcher that exits 0 faster than this handed the URL to an already
# running instance (chromium, soffice, xdg-open) rather than failing.
HANDOFF_SECS=5

log() {
  # Write to file + stderr separately (not via `tee`) so set -e doesn't bail
  # the script if either sink fails; logging must never be load-bearing.
  local msg
  msg="[viewdoc $(date -u +%H:%M:%S)] $*"
  printf '%s\n' "$msg" >> "$LOG" 2>/dev/null || true
  printf '%s\n' "$msg" >&2 || true
}

# status <phase> [app] [error] — records the outcome of this request for the
# sidecar's GET /status. Never load-bearing, like log().
status() {
  if {
    printf 'phase=%s\n' "$1"
    printf 'url=%s\n' "$(printf '%s' "$url" | tr '\n' ' ')"
    printf 'app=%s\n' "${2:-}"
    printf 'error=%s\n' "${3:-}"
    printf 'updated=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  } > "$STATUS.$$" 2>/dev/null; then
    mv -f "$STATUS.$$" "$STATUS" 2>/dev/null || true
  fi
}

[ -r "$QUERY_ENV" ] || { log "no $QUERY_ENV; nothing to do"; exit 0; }
# shellcheck disable=SC1090
. "$QUERY_ENV"

url="${VIEWDOC_Q_URL:-}"
if [ -z "$url" ]; then
  log "VIEWDOC_Q_URL empty; skipping"
  exit 0
fi

# $STATE is "<pid> <running|handoff|exited> [rc]" for the launch that wrote
# $LAST (rc once it has exited).
# The URL counts as still open while that launch's process is alive, or when
# it handed off to an existing instance (whose window we can't track). A
# crashed or closed app ("exited") lets the same URL open again.
is_open() {
  local pid state
  read -r pid state _ < "$STATE" 2>/dev/null || return 1
  case "$state" in
    handoff) return 0 ;;
    running) kill -0 "$pid" 2>/dev/null ;;
    *)       return 1 ;;
  esac
}

# Every app below takes the URL as a bare argument, so a value like
# "--version.mp4" would be parsed as an option. Only real URLs get through.
case "$url" in
  http://*|https://*) ;;
  *) log "not an http(s) URL, refusing: $url"; status refused "" "not an http(s) URL"; exit 0 ;;
esac

if [ -r "$LAST" ] && [ "$(cat "$LAST")" = "$url" ] && is_open; then
  log "dedup hit, $url already open"
  status deduped
  exit 0
fi

# Downloads get unique names (two overlapping requests can't clobber each
# other) and are pruned after a day.
find /tmp -maxdepth 1 -name 'viewdoc-dl.*' -mmin +1440 -delete 2>/dev/null || true
download() {
  local f
  f="$(mktemp --suffix=".$ext" /tmp/viewdoc-dl.XXXXXXXX)"
  log "download $url -> $f"
  status downloading "$app"
  if curl -fsSL --max-time 60 -o "$f" "$url"; then
    printf '%s' "$f"
  else
    log "download failed url=$url"
    status failed "$app" "download failed"
    rm -f "$f"
    return 1
  fi
}

lower="$(printf '%s' "$url" | tr '[:upper:]' '[:lower:]')"
path="${lower%%\?*}"; path="${path%%#*}"
ext="${path##*.}"

# dispatch <app> names the app this request goes to (for logs + status).
app=""
dispatch() {
  app="$1"
  log "dispatch=$app ext=$ext url=$url"
}

run() {
  log "exec: $*"
  # tee fans the launched app's combined stdout/stderr to both the single
  # log file and the sidecar's stderr — keeps the file as the durable
  # record AND surfaces output in `docker logs <viewer>` as it happens.
  # setsid detaches the app from this shell so the sidecar returns promptly.
  # When the app exits, the subshell records how — but only if no newer
  # launch has taken over $STATE meanwhile.
  (
    start=$SECONDS
    setsid "$@" </dev/null 2>&1 | tee -a "$LOG" >&2
    rc=${PIPESTATUS[0]}
    state=exited
    if [ "$rc" -eq 0 ] && [ $((SECONDS - start)) -lt "$HANDOFF_SECS" ]; then
      state=handoff
    fi
    owner=""
    read -r owner _ < "$STATE" 2>/dev/null || true
    if [ "$owner" = "$BASHPID" ]; then
      printf '%s %s %s\n' "$BASHPID" "$state" "$rc" > "$STATE"
    fi
    log "exit: $1 rc=$rc state=$state"
  ) &
  # Recorded only once something actually launched, so a failed download
  # doesn't make the next identical request a no-op.
  printf '%s %s\n' "$!" running > "$STATE"
  printf '%s' "$url" > "$LAST"
  status launched "${app:-$1}"
}

case "$ext" in
  mp4|mkv|webm|mov|avi|mpeg|mpg|m4v|3gp|3g2|ts|mts|m2ts|vob|wmv|asf|divx|ogv|mp3|wav|flac|ogg|m4a|aac|opus|mka|wma|aiff|ape|mid)
    dispatch vlc
    # --no-qt-privacy-ask suppresses VLC's first-run "Privacy and Network
    # Access Policy" modal, which otherwise blocks playback until clicked.
    run vlc --no-qt-privacy-ask --no-video-title-show "$url"
    ;;
  doc|docx|odt|rtf|xls|xlsx|ods|csv|ppt|pptx|odp|odg|vsd|vsdx|pub|pages|key|numbers)
    dispatch soffice
    # soffice handles http(s) URLs natively via UCB. --view opens read-only,
    # --norestore kills the "recover document?" prompt after a previous
    # session, --nologo suppresses the splash. Visio / Publisher / Apple
    # iWork files go through LibreOffice's libvisio / libmspub / libetonyek.
    run soffice --view --norestore --nologo "$url"
    ;;
  zip|7z|rar|tar|gz|tgz|bz2|tbz2|xz|txz|zst|tzst)
    # Archive managers want a local file, not a URL — download first, then
    # open. curl runs synchronously here because the sidecar already forked
    # viewdoc.sh as a separate process, so blocking here doesn't delay the
    # sidecar's HTTP response. RAR5 needs unrar (p7zip-full only reads RAR4).
    dispatch xarchiver
    if tmpfile="$(download)"; then
      run xarchiver "$tmpfile"
    fi
    ;;
  pcap|pcapng|cap)
    dispatch wireshark
    if tmpfile="$(download)"; then
      run wireshark -r "$tmpfile"
    fi
    ;;
  eml|msg)
    # claws-mail has no "open this file" flag that combines with
    # --alternate-config-dir (a bare file:// is only honoured when it is the
    # sole argument), so the message goes into a one-message MH mailbox
    # inside an isolated config dir and --select jumps straight to it. The
    # stub account keeps account_get_list() non-empty, which is the
    # condition that suppresses the first-run wizard. Both files are
    # rewritten every time because claws-mail saves its own copies on exit.
    cfgdir="/tmp/viewdoc-claws"
    inbox="$cfgdir/Mail/inbox"
    dispatch claws-mail
    if tmpfile="$(download)"; then
      # Outlook .msg is an OLE compound file claws-mail can't read; msgconvert
      # (libemail-outlook-message-perl) rewrites it as MIME first.
      if [ "$ext" = msg ]; then
        eml="${tmpfile%.msg}.eml"
        if ! msgconvert --outfile "$eml" "$tmpfile" >&2; then
          log "msgconvert failed url=$url"
          status failed "$app" "msgconvert failed"
          exit 0
        fi
        tmpfile="$eml"
      fi
      # A running instance would receive --select over its socket but not
      # rescan the swapped-out mailbox, so start fresh.
      if pkill -x claws-mail; then sleep 1; fi
      rm -rf "$inbox"
      mkdir -p "$inbox"
      cp "$tmpfile" "$inbox/1"
      cat > "$cfgdir/folderlist.xml" <<XML
<?xml version="1.0" encoding="UTF-8"?>
<folderlist>
  <folder type="mh" name="viewdoc" path="$cfgdir/Mail" collapsed="0">
    <folderitem type="normal" name="viewdoc" path="" />
    <folderitem type="inbox" name="inbox" path="inbox" />
  </folder>
</folderlist>
XML
      cat > "$cfgdir/accountrc" <<'CFG'
[Account: 1]
account_name=local
name=Viewdoc
address=viewdoc@example.invalid
protocol=2
recv_at_getall=0
CFG
      run claws-mail --alternate-config-dir "$cfgdir" --select "$inbox/1"
    fi
    ;;
  tif|tiff|heic|heif|avif|ico|tga)
    # Formats Chromium won't render. ristretto decodes via gdk-pixbuf, with
    # heif-gdk-pixbuf / libavif-gdk-pixbuf adding HEIC and AVIF.
    dispatch ristretto
    if tmpfile="$(download)"; then
      run ristretto "$tmpfile"
    fi
    ;;
  epub|djvu|xps|oxps|cbz|cbr|cb7)
    dispatch atril
    if tmpfile="$(download)"; then
      run atril "$tmpfile"
    fi
    ;;
  txt|log|json|yaml|yml|xml|md|ini|conf|cfg|toml)
    # Downloaded rather than handed to Chromium: servers often send these as
    # application/octet-stream, which Chromium saves instead of showing.
    dispatch mousepad
    if tmpfile="$(download)"; then
      run mousepad "$tmpfile"
    fi
    ;;
  stl|obj|ply|glb|gltf|3ds|vtk|vtp)
    log "dispatch=f3d ext=$ext url=$url"
    if tmpfile="$(download)"; then
      run f3d "$tmpfile"
    fi
    ;;
  dxf)
    dispatch librecad
    if tmpfile="$(download)"; then
      run librecad "$tmpfile"
    fi
    ;;
  ttf|otf|ttc)
    dispatch gnome-font-viewer
    if tmpfile="$(download)"; then
      run gnome-font-viewer "$tmpfile"
    fi
    ;;
  bin|exe|dll|so|o|elf|dat|dmp)
    dispatch ghex
    if tmpfile="$(download)"; then
      run ghex "$tmpfile"
    fi
    ;;
  *)
    # Chromium with --no-sandbox: Kasm's firefox 144 trips over a stale
    # profiles.ini Locked=1, and user namespaces are blocked in the container.
    # --user-data-dir isolates from any chromium the desktop autostarts
    # (webtop ships one wired to /config/.config/chromium, whose
    # SingletonLock embeds the previous container's hostname — recreating
    # the container leaves a stale lock that wedges every new launch).
    for b in chromium chromium-browser google-chrome chrome; do
      if command -v "$b" >/dev/null 2>&1; then
        dispatch "$b"
        run "$b" --no-sandbox --user-data-dir=/tmp/viewdoc-chromium --new-window "$url"
        exit 0
      fi
    done
    dispatch xdg-open
    run xdg-open "$url"
    ;;
esac
