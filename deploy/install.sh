#!/usr/bin/env bash
# Lightspeak server installer for Debian/Ubuntu (VPS or home server).
#
# Installs and configures: lightspeak-server (Go binary), LiveKit (SFU),
# Caddy (HTTPS via Let's Encrypt), systemd units, a daily backup timer and
# – if ufw is present – firewall rules. Idempotent: run it again to change
# settings or to install a new server binary; secrets are kept.
#
#   sudo bash install.sh --version latest                 # download and install
#   sudo bash install.sh --server-binary ./lightspeak-server-linux-amd64 \
#                        [--web-archive ./lightspeak-web.tar.gz]
#   sudo bash install.sh --config answers.env --yes       # unattended
#
# --version downloads the server and the browser client from the release
# source and only accepts files listed in a SHA256SUMS signed with
# RELEASE_PUBKEY. Afterwards `sudo lightspeak-admin update` does the same,
# with a database backup, a health check and an automatic rollback.
#
# answers.env (all optional, otherwise asked or defaulted):
#   LS_PUBLIC_HOST=chat.example.org     LS_RTC_HOST=rtc.chat.example.org
#   LS_SERVER_NAME="Our server"         LS_SIGNUP=admin|invite|closed
#   LS_DEPLOY_USER=lsdeploy  # may run lightspeak-admin and restart services over SSH;
#                            # created if missing (SSH key only, no password).
#                            # Empty or absent = no such user.
#   LS_DEPLOY_KEY="ssh-ed25519 AAAA… me@pc"   # public key for that user
#   LS_CADDY_SOURCE=official|distro  # default official (current Caddy release)
#   LS_FIREWALL=yes|no     # add ufw rules (if ufw is installed)
#   LS_UFW_ENABLE=yes      # also switch an inactive ufw on (never implied by --yes)
#   LS_ACME_EMAIL=you@example.org  LS_MAX_UPLOAD_MB=200  LS_STORAGE_QUOTA_GB=4
#   LS_MAX_VOICE_PARTICIPANTS=25
#   Every server setting from deploy/server.env.example, e.g. LS_MAX_SPACES=5.
#   LS_RELEASE_URL, LS_RELEASE_PUBKEY: your own release source (fork, mirror).
#   Unknown keys stop the installer.
#
# Nothing outside the Lightspeak paths is changed except: the Caddy package
# (installed if missing), one import line in /etc/caddy/Caddyfile (backed up
# first), a systemd drop-in that moves Caddy's admin API from localhost:2019
# to a socket only the caddy user can reach (caddy.service.d/lightspeak-admin.conf),
# ufw rules (only if you agree), the deploy user and
# /etc/sudoers.d/lightspeak (only with LS_DEPLOY_USER).
#
# Use a dedicated deploy user: whoever controls it can replace the server
# binary and the installer offered on the landing page.
set -euo pipefail
umask 027

# ---------- pinned versions ----------
LIVEKIT_VERSION="1.13.11"
declare -A LIVEKIT_SHA256=(
  [amd64]="1e9ee90ca301e1842b2887fc77cb122759756a9c8aeb4334ee91de3325ec43fb"
  [arm64]="2af6ce091d6b3c79d1677f8ff1bbb91a8007f40b2325c059b52d38383c0830b6"
)

# ---------- paths ----------
APP_DIR=/opt/lightspeak                       # releases/<ver>, current -> releases/<ver>
LK_DIR=/usr/local/lib/lightspeak-livekit      # <ver>/livekit-server (root-owned)
ETC=/etc/lightspeak
DATA=/var/lib/lightspeak
UNIT_DIR=/etc/systemd/system
CADDY_SITE=/etc/caddy/lightspeak.caddy
ADMIN_BIN=/usr/local/bin/lightspeak-admin
INSTALLER_COPY=/usr/local/lib/lightspeak/install.sh  # used by lightspeak-admin
# A new release is prepared here, not below releases/: that directory belongs
# to the deploy user, and root must not work inside it.
STAGE=/usr/local/lib/lightspeak/stage

# ---------- release source ----------
# Layout of a GitHub repository: <url>/latest/download/<file> and
# <url>/download/v<version>/<file>. RELEASE_PUBKEY is client/src-tauri/updater.pub
# (release.ps1 checks that the two match).
RELEASE_URL_DEFAULT=https://github.com/syntstar/lightspeak/releases
RELEASE_PUBKEY_DEFAULT=dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IEYzMjhBMDA4RjhBMUJFMTAKUldRUXZxSDRDS0FvODd4Y3ZVV3IzUkdhVFNTUFFrWHNjV1p5VkcxK1BPVURWTDJwdk13M1NtVGYK

# ---------- settings ----------
# Written to server.env when they come from an answers file; the template with
# all of them is deploy/server.env.example.
SERVER_SETTINGS="LS_INVITES LS_MAX_SPACES LS_MAX_MESSAGE_CHARS LS_MAX_SPACE_MEMBERS LS_MAX_SPACE_CHANNELS LS_MAX_SPACE_ROLES LS_RETENTION_DAYS LS_MIN_CLIENT_VERSION LS_DOWNLOAD_URL LS_EXTRA_ORIGINS LS_RELEASE_URL LS_RELEASE_PUBKEY"
# Only read by this script (some of them end up in server.env as managed lines).
INSTALLER_SETTINGS="LS_PUBLIC_HOST LS_RTC_HOST LS_SERVER_NAME LS_SIGNUP LS_DEPLOY_USER LS_DEPLOY_KEY LS_CADDY_SOURCE LS_FIREWALL LS_UFW_ENABLE LS_ACME_EMAIL LS_MAX_UPLOAD_MB LS_STORAGE_QUOTA_GB LS_MAX_VOICE_PARTICIPANTS"

CONFIG_FILE="" BINARY="" WEB_ARCHIVE="" VERSION="" ASSUME_YES=0 UPDATE_ONLY=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG_FILE="$2"; shift 2 ;;
    --server-binary) BINARY="$2"; shift 2 ;;
    --web-archive) WEB_ARCHIVE="$2"; shift 2 ;;
    --version) VERSION="$2"; shift 2 ;;
    --update) UPDATE_ONLY=1; shift ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    -h|--help) sed -n '2,/^set -euo/p' "$0" | sed '$d'; exit 0 ;;
    *) echo "Unknown option: $1" >&2; exit 2 ;;
  esac
done
[[ -z "$BINARY" || -z "$VERSION" ]] || { echo "Use either --server-binary or --version." >&2; exit 2; }
# The deploy user may run `lightspeak-admin update` as root: nothing it passes
# may name a file (an answers file is executed as shell code).
[[ $UPDATE_ONLY == 0 || -z "$CONFIG_FILE$BINARY$WEB_ARCHIVE" ]] || { echo "--update only takes --version and --yes." >&2; exit 2; }
[[ -z "$VERSION" || $VERSION == latest || $VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "--version must be latest or x.y.z" >&2; exit 2; }

say() { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m! %s\033[0m\n' "$*" >&2; }
die() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }
ask() { # ask VAR "Question" default
  local var=$1 q=$2 def=${3-} ans
  if [[ -n "${!var-}" ]]; then return; fi
  if [[ $ASSUME_YES == 1 ]]; then printf -v "$var" '%s' "$def"; return; fi
  read -r -p "$q${def:+ [$def]}: " ans </dev/tty || true
  printf -v "$var" '%s' "${ans:-$def}"
}
confirm() { # confirm "Question" -> 0 = yes
  [[ $ASSUME_YES == 1 ]] && return 0
  local ans; read -r -p "$1 [y/N]: " ans </dev/tty || true
  [[ $ans =~ ^[yYjJ] ]]
}
strip_quotes() { sed 's/^"\(.*\)"$/\1/'; }
envval() { [[ -f "$1" ]] && sed -n "s/^$2=//p" "$1" | tail -n1 | strip_quotes || true; }
healthy() {
  for _ in $(seq 1 20); do
    curl -fsS --max-time 2 http://127.0.0.1:8080/healthz >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

# verify_signature <file> <file.sig>: a Tauri/minisign signature (Ed25519 over
# the BLAKE2b-512 of the file) against RELEASE_PUBKEY, with nothing but openssl.
verify_signature() {
  local d rc=0 pub sig
  d=$(mktemp -d)
  pub=$(base64 -d <<<"$RELEASE_PUBKEY" 2>/dev/null | sed -n 2p) || true
  sig=$(base64 -d "$2" 2>/dev/null | sed -n 2p) || true
  base64 -d <<<"$pub" >"$d/pub" 2>/dev/null || true
  base64 -d <<<"$sig" >"$d/sig" 2>/dev/null || true
  if [[ $(wc -c <"$d/pub") -ne 42 || $(wc -c <"$d/sig") -ne 74 || $(head -c 2 "$d/sig") != ED ]] ||
     ! cmp -s <(tail -c +3 "$d/pub" | head -c 8) <(tail -c +3 "$d/sig" | head -c 8); then
    rc=1 # not a signature of this key
  else
    { printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; tail -c 32 "$d/pub"; } >"$d/pub.der"
    tail -c 64 "$d/sig" >"$d/raw"
    openssl dgst -blake2b512 -binary "$1" >"$d/digest"
    openssl pkey -pubin -inform DER -in "$d/pub.der" -out "$d/pub.pem" 2>/dev/null || rc=1
    openssl pkeyutl -verify -pubin -inkey "$d/pub.pem" -rawin -in "$d/digest" -sigfile "$d/raw" >/dev/null 2>&1 || rc=1
  fi
  rm -rf "$d"
  return $rc
}

# fetch_release <version|latest> <dir>: downloads what this machine needs and
# verifies it. Leaves lightspeak-server-linux-<arch>, lightspeak-web.tar.gz
# (if the release has one) and install.sh in <dir>.
fetch_release() {
  local want=$1 dir=$2 base proto=(--proto '=https' --proto-redir '=https' --tlsv1.2) f
  [[ -n "$RELEASE_PUBKEY" ]] || die "No release key configured (LS_RELEASE_PUBKEY)."
  base="$RELEASE_URL/download/v$want"
  [[ $want != latest ]] || base="$RELEASE_URL/latest/download"
  [[ $RELEASE_URL != http://127.0.0.1* ]] || proto=() # local test mirror
  get() { curl -fsSL "${proto[@]}" --retry 2 --max-time 900 -o "$dir/$1" "$base/$1"; }
  say "Downloading release $want from $RELEASE_URL"
  if ! get SHA256SUMS || ! get SHA256SUMS.sig; then
    die "No release found at $base. Repository still private or the version does not exist? Otherwise use --server-binary."
  fi
  verify_signature "$dir/SHA256SUMS" "$dir/SHA256SUMS.sig" || die "SHA256SUMS is not signed with the Lightspeak release key – aborting."
  for f in "lightspeak-server-linux-$ARCH" lightspeak-web.tar.gz install.sh; do
    if grep -Eq "^[0-9a-f]{64} [ *]$f\$" "$dir/SHA256SUMS"; then
      get "$f" || die "Download failed: $f"
      (cd "$dir" && grep -E "^[0-9a-f]{64} [ *]$f\$" SHA256SUMS | sha256sum -c --quiet -) || die "Checksum mismatch: $f – aborting."
    elif [[ $f == lightspeak-server-* ]]; then
      die "The release has no server for $ARCH."
    fi
  done
  BINARY="$dir/lightspeak-server-linux-$ARCH"
  WEB_ARCHIVE=""
  [[ ! -f "$dir/lightspeak-web.tar.gz" ]] || WEB_ARCHIVE="$dir/lightspeak-web.tar.gz"
}

# stage_release: prepares $BINARY (and $WEB_ARCHIVE) as an unused directory
# below releases/ and sets NEW_VER. A release without a browser client keeps
# the one of the running release.
stage_release() {
  local inc=$STAGE cur
  rm -rf "$inc"
  install -d -m 0755 -o root -g root "$(dirname "$inc")" "$inc"
  install -m 0755 -o root -g root "$BINARY" "$inc/lightspeak-server"
  NEW_VER=$(runuser -u lightspeak -- "$inc/lightspeak-server" version 2>/dev/null || true)
  [[ $NEW_VER =~ ^[A-Za-z0-9._+-]+$ && $NEW_VER != dev ]] || NEW_VER="manual-$(date +%Y%m%d%H%M%S)"
  if [[ -n "$WEB_ARCHIVE" ]]; then
    install -d -m 0755 "$inc/web"
    tar -xzf "$WEB_ARCHIVE" -C "$inc/web" --no-same-owner --no-same-permissions
    [[ -f "$inc/web/index.html" ]] || die "The browser client archive has no index.html."
    chmod -R u=rwX,go=rX "$inc/web"
  else
    cur=$(readlink -f "$APP_DIR/current" 2>/dev/null || true)
    [[ -z "$cur" || ! -d "$cur/web" ]] || cp -a --no-preserve=ownership "$cur/web" "$inc/web"
  fi
}

# activate_release: backup, atomic switch, restart, health check, automatic
# rollback, then keeps the new release and the one before it.
activate_release() {
  local prev dir="$APP_DIR/releases/$NEW_VER" keep d
  prev=$(readlink "$APP_DIR/current" 2>/dev/null || true)
  if [[ -n "$prev" && -e "$APP_DIR/current/lightspeak-server" ]]; then
    say "Backing up the database"
    "$ADMIN_BIN" backup --keep 3 >/dev/null || die "Backup failed – nothing was changed."
  fi
  rm -rf "$dir"
  mv -T "$STAGE" "$dir"
  chown -hR "$(stat -c %U:%G "$APP_DIR/releases")" "$dir"
  switch_release "releases/$NEW_VER"
  say "Starting server $NEW_VER"
  restart_server
  if ! healthy; then
    journalctl -u lightspeak-server -n 15 --no-pager >&2 || true
    if [[ -n "$prev" && "$prev" != "releases/$NEW_VER" && -e "$APP_DIR/$prev/lightspeak-server" ]]; then
      switch_release "$prev"
      restart_server
      healthy && warn "Rolled back to $prev." || warn "Rollback to $prev is not healthy either!"
      rm -rf "$dir"
      warn "A database backup from before the update is in $DATA/backups."
    fi
    die "Server $NEW_VER did not start."
  fi
  keep=${prev#releases/}
  [[ "$keep" != "$NEW_VER" ]] || keep=$(cd "$APP_DIR/releases" && ls -1dt -- */ 2>/dev/null | sed 's#/$##' | grep -vxF -- "$NEW_VER" | head -n1 || true)
  for d in "$APP_DIR"/releases/*/; do
    d=$(basename "$d")
    [[ "$d" == "$NEW_VER" || "$d" == "$keep" ]] || rm -rf -- "${APP_DIR:?}/releases/$d"
  done
}
# A crash loop or quick re-runs must not trip systemd's start limit: that
# would block exactly the restart that repairs the server.
restart_server() {
  systemctl reset-failed lightspeak-server.service 2>/dev/null || true
  systemctl restart lightspeak-server.service
}
switch_release() {
  ln -sfn "$1" "$APP_DIR/current.tmp"
  chown -h "$(stat -c %U:%G "$APP_DIR/releases")" "$APP_DIR/current.tmp"
  mv -T "$APP_DIR/current.tmp" "$APP_DIR/current"
}

# check_config <binary>: lets the server judge server.env before it is used.
# Older binaries do not know the command; the running release is then trusted.
check_config() {
  { runuser -u lightspeak -- "$1" 2>&1 || true; } | grep -q check-config || return 0
  # A clean environment: answers-file values are exported in this script.
  env -i PATH="$PATH" bash -c 'set -a; . "$1"; set +a; exec runuser -u lightspeak -- "$2" check-config' _ "$ETC/server.env" "$1" >/dev/null
}

# Whatever stops a run: an unused stage must not be activated by a later one.
TMP_RELEASE=""
cleanup() { rm -rf "$STAGE" "$TMP_RELEASE"; }

# version_older <a> <b>: a is an older x.y.z than b.
version_older() { [[ $1 != "$2" && $(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1) == "$1" ]]; }

# update_server: `lightspeak-admin update [--version x.y.z]`
update_server() {
  [[ -f $ETC/server.env ]] || die "Lightspeak is not installed here."
  local cur got want=${VERSION:-latest}
  TMP_RELEASE=$(mktemp -d); trap cleanup EXIT
  fetch_release "$want" "$TMP_RELEASE"
  stage_release
  got=$NEW_VER
  cur=$(basename "$(readlink "$APP_DIR/current" 2>/dev/null || echo none)")
  # A signature only proves that a file is ours, not that it is the newest or
  # the one asked for: a mirror must not be able to serve an old release.
  [[ $want == latest || $want == "$got" ]] || die "Asked for $want, but the signed release contains $got."
  if [[ $cur == "$got" ]]; then
    say "Already on $got."
    return
  fi
  if [[ $cur =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && version_older "$got" "$cur"; then
    if [[ $want == latest ]]; then
      die "\"latest\" is $got, older than the installed $cur – refusing to downgrade."
    fi
    confirm "Downgrade from $cur to $got? Database changes of $cur are not undone." || die "Aborted."
  fi
  activate_release
  if [[ -f "$TMP_RELEASE/install.sh" ]] && ! cmp -s "$TMP_RELEASE/install.sh" "$INSTALLER_COPY"; then
    install -m 0755 -o root -g root "$TMP_RELEASE/install.sh" "$INSTALLER_COPY"
    warn "This release has a new installer (Caddy/LiveKit/systemd). Apply it when nobody is in a call: sudo lightspeak-admin reconfigure"
  fi
  say "Lightspeak server $got is running."
}

valid_host() { [[ $1 =~ ^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$|^([a-zA-Z0-9-]+\.)+sslip\.io$|^([a-zA-Z0-9-]+\.)+nip\.io$ ]]; }

# ---------- preconditions ----------
[[ $EUID -eq 0 ]] || die "Please run as root: sudo bash $0"
command -v systemctl >/dev/null || die "systemd is required."
command -v apt-get >/dev/null || die "Only Debian/Ubuntu (apt) are supported."
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;;
  *) die "Unsupported CPU architecture: $(uname -m)" ;;
esac
if [[ -n "$CONFIG_FILE" ]]; then
  [[ -f "$CONFIG_FILE" ]] || die "Config file not found: $CONFIG_FILE"
  while IFS= read -r k; do
    [[ " $SERVER_SETTINGS $INSTALLER_SETTINGS " == *" $k "* ]] || die "Unknown setting in $CONFIG_FILE: $k"
  done < <(sed -nE 's/^[[:space:]]*(export[[:space:]]+)?([A-Za-z_][A-Za-z0-9_]*)=.*/\2/p' "$CONFIG_FILE")
  set -a
  # shellcheck source=/dev/null
  . "$CONFIG_FILE"
  set +a
fi
for f in "$BINARY" "$WEB_ARCHIVE"; do
  [[ -z "$f" || -f "$f" ]] || die "File not found: $f"
done
[[ -z "$BINARY" ]] || BINARY=$(readlink -f "$BINARY")
[[ -z "$WEB_ARCHIVE" ]] || WEB_ARCHIVE=$(readlink -f "$WEB_ARCHIVE")
: "${LS_RELEASE_URL:=$(envval "$ETC/server.env" LS_RELEASE_URL)}"
: "${LS_RELEASE_PUBKEY:=$(envval "$ETC/server.env" LS_RELEASE_PUBKEY)}"
RELEASE_URL=${LS_RELEASE_URL:-$RELEASE_URL_DEFAULT}
RELEASE_PUBKEY=${LS_RELEASE_PUBKEY:-$RELEASE_PUBKEY_DEFAULT}
RELEASE_URL=${RELEASE_URL%/}
[[ $RELEASE_URL == https://* || $RELEASE_URL == http://127.0.0.1* ]] || die "LS_RELEASE_URL must use https."

if [[ $UPDATE_ONLY == 1 ]]; then
  update_server
  exit 0
fi

# Keep earlier answers when re-running.
if [[ -f $ETC/server.env ]]; then
  prev() { envval "$ETC/server.env" "$1"; }
  : "${LS_PUBLIC_HOST:=$(prev LS_PUBLIC_URL | sed 's#^https://##')}"
  : "${LS_RTC_HOST:=$(prev LS_LIVEKIT_URL | sed 's#^wss://##')}"
  : "${LS_SERVER_NAME:=$(prev LS_SERVER_NAME)}"
  : "${LS_SIGNUP:=$(prev LS_SIGNUP)}"
  : "${LS_MAX_UPLOAD_MB:=$(prev LS_MAX_UPLOAD_MB)}"
  : "${LS_STORAGE_QUOTA_GB:=$(prev LS_STORAGE_QUOTA_GB)}"
  : "${LS_MAX_VOICE_PARTICIPANTS:=$(prev LS_MAX_VOICE_PARTICIPANTS)}"
fi
: "${LS_MAX_UPLOAD_MB:=200}" "${LS_STORAGE_QUOTA_GB:=4}" "${LS_MAX_VOICE_PARTICIPANTS:=25}"
[[ $LS_MAX_UPLOAD_MB =~ ^[0-9]+$ && $LS_STORAGE_QUOTA_GB =~ ^[0-9]+$ && $LS_MAX_VOICE_PARTICIPANTS =~ ^[0-9]+$ ]] || die "LS_MAX_UPLOAD_MB, LS_STORAGE_QUOTA_GB and LS_MAX_VOICE_PARTICIPANTS must be numbers."
# Settings from an answers file go into server.env. Quoted there, so only a
# value that cannot break out of the quotes is accepted.
answered=""
for k in $SERVER_SETTINGS; do
  [[ -n "${!k+x}" ]] || continue
  [[ ${!k} != *[\"\$\`\\]* && ${!k} != *$'\n'* ]] || die "$k must not contain any of: \" \$ \` \\"
  answered+=" $k"
done

say "Lightspeak server setup"
echo "Two public hostnames are needed, both pointing to this machine (A/AAAA record)."
echo "Without a domain you can use sslip.io, e.g. chat.203-0-113-5.sslip.io and rtc.203-0-113-5.sslip.io."
ask LS_PUBLIC_HOST "Hostname for the app (API, invite links)" ""
valid_host "$LS_PUBLIC_HOST" || die "Invalid hostname: '$LS_PUBLIC_HOST'"
ask LS_RTC_HOST "Hostname for voice/video signalling" "rtc.$LS_PUBLIC_HOST"
valid_host "$LS_RTC_HOST" || die "Invalid hostname: '$LS_RTC_HOST'"
[[ "$LS_RTC_HOST" != "$LS_PUBLIC_HOST" ]] || die "The two hostnames must differ."
ask LS_SERVER_NAME "Server name shown before sign-in (max. 64 characters)" "$LS_PUBLIC_HOST"
[[ ${#LS_SERVER_NAME} -le 64 && ! "$LS_SERVER_NAME" =~ [\"\$\`\\] && "$LS_SERVER_NAME" != *$'\n'* ]] || die "Server name too long or contains one of: \" \$ \` \\"
ask LS_SIGNUP "Who may create accounts? admin = only invites from server admins, invite = any invite, closed" "admin"
[[ $LS_SIGNUP =~ ^(admin|invite|closed)$ ]] || die "LS_SIGNUP must be admin, invite or closed."
# Only needed to update or manage the server remotely without root. On a re-run
# the owner of releases/ shows the earlier choice (root = none).
if [[ -z "${LS_DEPLOY_USER+x}" ]]; then
  if [[ -d $APP_DIR/releases ]]; then
    LS_DEPLOY_USER=$(stat -c %U "$APP_DIR/releases")
    [[ $LS_DEPLOY_USER != root ]] || LS_DEPLOY_USER=""
  else
    LS_DEPLOY_USER=""
    [[ $ASSUME_YES == 1 ]] || read -r -p "Separate account for remote updates over SSH (optional, e.g. lsdeploy; empty = none): " LS_DEPLOY_USER </dev/tty || true
  fi
fi
deploy_new=0
if [[ -n "$LS_DEPLOY_USER" ]]; then
  [[ $LS_DEPLOY_USER =~ ^[a-z_][a-z0-9_-]{0,31}$ ]] || die "Invalid user name: '$LS_DEPLOY_USER'"
  [[ "$LS_DEPLOY_USER" != root && "$LS_DEPLOY_USER" != lightspeak && "$LS_DEPLOY_USER" != livekit ]] || die "Deploy user must be a separate account."
  id "$LS_DEPLOY_USER" >/dev/null 2>&1 || deploy_new=1
  [[ $deploy_new == 0 ]] || ask LS_DEPLOY_KEY "SSH public key for $LS_DEPLOY_USER (one line: ssh-ed25519 AAAA…)" ""
  if [[ -n "${LS_DEPLOY_KEY-}" ]]; then
    [[ $LS_DEPLOY_KEY =~ ^(ssh-ed25519|ecdsa-sha2-nistp(256|384|521)|ssh-rsa|sk-ssh-ed25519@openssh\.com)\ [A-Za-z0-9+/=]+(\ [[:print:]]*)?$ ]] || die "LS_DEPLOY_KEY is not an SSH public key line."
  elif [[ $deploy_new == 1 ]]; then
    die "The new user '$LS_DEPLOY_USER' needs an SSH public key (LS_DEPLOY_KEY)."
  fi
fi
: "${LS_CADDY_SOURCE:=official}"
[[ $LS_CADDY_SOURCE =~ ^(official|distro)$ ]] || die "LS_CADDY_SOURCE must be official or distro."
ask LS_ACME_EMAIL "E-mail for Let's Encrypt notices (optional)" ""

# Something else on 80/443 would block Caddy.
busy=$(ss -Hltnp '( sport = :80 or sport = :443 )' 2>/dev/null | grep -v caddy || true)
[[ -z "$busy" ]] || die "Ports 80/443 are used by another program; Lightspeak needs them for Caddy:"$'\n'"$busy"

# DNS check (warning only: the record may still propagate).
pub_ip=$(curl -fsS -4 --max-time 5 https://api.ipify.org 2>/dev/null || true)
for h in "$LS_PUBLIC_HOST" "$LS_RTC_HOST"; do
  ips=$({ getent ahostsv4 "$h" || true; } | awk '{print $1}' | sort -u | paste -sd' ' -)
  if [[ -z "$ips" ]]; then warn "$h does not resolve yet."
  elif [[ -n "$pub_ip" && " $ips " != *" $pub_ip "* ]]; then warn "$h resolves to $ips, but this server's public IPv4 is $pub_ip."
  fi
done

ufw_state=none
command -v ufw >/dev/null && ufw_state=$(ufw status 2>/dev/null | head -n1 | awk '{print $2}')

server_src=${BINARY:-keep current / install later}
[[ -z "$VERSION" ]] || server_src="release $VERSION from $RELEASE_URL"
echo
say "Summary"
cat <<EOF
  App:              https://$LS_PUBLIC_HOST
  Voice signalling: wss://$LS_RTC_HOST
  Server name:      $LS_SERVER_NAME
  Sign-up:          $LS_SIGNUP
  Deploy user:      ${LS_DEPLOY_USER:-–}$([[ $deploy_new == 1 ]] && echo " (new, SSH key only)")
  LiveKit:          v$LIVEKIT_VERSION ($ARCH)
  Server:           $server_src
  Firewall (ufw):   $ufw_state
  Public ports:     80/tcp, 443/tcp+udp (Caddy), 7881/tcp + 7882/udp (voice/video)
EOF
confirm "Continue?" || die "Aborted, nothing changed."

# ---------- packages ----------
say "Packages"
APT_LOG=/var/log/lightspeak-install.log
apt_install() { DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "$@" >>"$APT_LOG" 2>&1 || die "apt-get install $* failed, see $APT_LOG"; }
need=()
for p in curl ca-certificates openssl sqlite3; do dpkg -s "$p" >/dev/null 2>&1 || need+=("$p"); done
if ! command -v caddy >/dev/null; then
  apt-get update -qq >>"$APT_LOG" 2>&1
  if [[ $LS_CADDY_SOURCE == distro ]] && apt-cache policy caddy | grep -q 'Candidate: [0-9]'; then
    need+=(caddy) # distribution package (often old)
  else
    say "Adding the official Caddy repository"
    command -v gpg >/dev/null || apt_install gnupg
    command -v curl >/dev/null || apt_install curl ca-certificates
    curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key | gpg --dearmor --yes -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt > /etc/apt/sources.list.d/caddy-stable.list
    chmod o+r /usr/share/keyrings/caddy-stable-archive-keyring.gpg /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -qq >>"$APT_LOG" 2>&1
    need+=(caddy)
  fi
fi
if ((${#need[@]})); then echo "  installing: ${need[*]}"; apt_install "${need[@]}"; fi

# ---------- users and directories ----------
say "Users and directories"
for u in lightspeak livekit; do
  id "$u" >/dev/null 2>&1 || useradd --system --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$u"
done
if [[ $deploy_new == 1 ]]; then
  # No password (locked); login only with the given SSH key.
  useradd --create-home --user-group --shell /bin/bash --comment "Lightspeak deploy" "$LS_DEPLOY_USER"
fi
if [[ -n "${LS_DEPLOY_KEY-}" ]]; then
  dhome=$(getent passwd "$LS_DEPLOY_USER" | cut -d: -f6)
  dgroup=$(id -gn "$LS_DEPLOY_USER")
  install -d -m 0700 -o "$LS_DEPLOY_USER" -g "$dgroup" "$dhome/.ssh"
  ak="$dhome/.ssh/authorized_keys"
  [[ -f "$ak" ]] || install -m 0600 -o "$LS_DEPLOY_USER" -g "$dgroup" /dev/null "$ak"
  grep -qxF "$LS_DEPLOY_KEY" "$ak" || printf '%s\n' "$LS_DEPLOY_KEY" >> "$ak"
fi
owner=${LS_DEPLOY_USER:-root}
ogrp=$(id -gn "$owner")
install -d -m 0755 -o "$owner" -g "$ogrp" "$APP_DIR"
install -d -m 0755 -o "$owner" -g "$ogrp" "$APP_DIR/releases" # binaries are public releases, not secret
# Also takes the releases away from a deploy user that was removed.
chown -hR "$owner":"$ogrp" "$APP_DIR"
install -d -m 0755 -o root -g root "$LK_DIR" "$(dirname "$INSTALLER_COPY")"
install -d -m 0751 -o root -g root "$ETC" # files inside are 0640 per service group
install -d -m 0750 -o lightspeak -g lightspeak "$DATA" "$DATA/data" "$DATA/files" "$DATA/backups" "$DATA/public"
install -d -m 0755 -o lightspeak -g lightspeak "$DATA/public/updates" "$DATA/public/downloads"
if [[ -n "$LS_DEPLOY_USER" ]]; then
  # The deploy user uploads installers/update manifests.
  chown "$LS_DEPLOY_USER":lightspeak "$DATA/public/updates" "$DATA/public/downloads"
  chmod 0755 "$DATA/public/updates" "$DATA/public/downloads"
  chmod 0751 "$DATA" "$DATA/public"
fi

# ---------- LiveKit ----------
lk_ver_dir="$LK_DIR/v$LIVEKIT_VERSION"
if [[ ! -x "$lk_ver_dir/livekit-server" ]]; then
  say "Downloading LiveKit v$LIVEKIT_VERSION"
  tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
  tgz="livekit_${LIVEKIT_VERSION}_linux_${ARCH}.tar.gz"
  curl -fsSL --max-time 300 -o "$tmp/$tgz" "https://github.com/livekit/livekit/releases/download/v$LIVEKIT_VERSION/$tgz"
  echo "${LIVEKIT_SHA256[$ARCH]}  $tmp/$tgz" | sha256sum -c --quiet - || die "LiveKit checksum mismatch – aborting."
  tar -xzf "$tmp/$tgz" -C "$tmp" livekit-server
  install -d -m 0755 "$lk_ver_dir"
  install -m 0755 "$tmp/livekit-server" "$lk_ver_dir/livekit-server"
  rm -rf "$tmp"; trap - EXIT
fi
# Old LiveKit versions are not needed.
find "$LK_DIR" -mindepth 1 -maxdepth 1 -type d ! -name "v$LIVEKIT_VERSION" -exec rm -rf {} +

# ---------- secrets (created once, never printed) ----------
say "Configuration"
rand() { openssl rand -base64 "$1" | tr -d '\n/+=' | cut -c1-"$2"; }
LK_KEY=$(envval "$ETC/server.env" LIVEKIT_API_KEY); LK_KEY=${LK_KEY:-API$(rand 24 16)}
LK_SECRET=$(envval "$ETC/server.env" LIVEKIT_API_SECRET); LK_SECRET=${LK_SECRET:-$(rand 64 48)}
FILE_SECRET=$(envval "$ETC/server.env" LS_FILE_URL_SECRET); FILE_SECRET=${FILE_SECRET:-$(rand 64 48)}

write() { # write <path> <owner:group> <mode>  (content on stdin, atomic)
  local tmpf; tmpf=$(mktemp "$1.XXXX"); cat > "$tmpf"
  chown "$2" "$tmpf"; chmod "$3" "$tmpf"; mv -f "$tmpf" "$1"
}

# ---------- server release ----------
# Downloaded and verified before any service is touched.
rm -rf "$STAGE" # a killed run must not leave something to activate
trap cleanup EXIT
if [[ -n "$VERSION" ]]; then
  TMP_RELEASE=$(mktemp -d)
  fetch_release "$VERSION" "$TMP_RELEASE"
fi
if [[ -n "$BINARY" ]]; then
  stage_release
  [[ -z "$VERSION" || $VERSION == latest || $VERSION == "$NEW_VER" ]] || die "Asked for $VERSION, but the signed release contains $NEW_VER."
fi

MANAGED="LS_PUBLIC_URL LS_LIVEKIT_URL LS_LIVEKIT_API_URL LS_ADDR LS_WEBHOOK_ADDR LS_DATA_DIR LS_SERVER_NAME LS_SIGNUP LS_TRUST_PROXY LS_MAX_UPLOAD_MB LS_STORAGE_QUOTA_GB LS_MAX_VOICE_PARTICIPANTS LIVEKIT_API_KEY LIVEKIT_API_SECRET LS_FILE_URL_SECRET"
custom=""
if [[ -f $ETC/server.env ]]; then # keep settings an admin added by hand (e.g. LS_RETENTION_DAYS)
  while IFS= read -r line; do
    k=${line%%=*}
    [[ $line =~ ^[A-Z_][A-Z0-9_]*= && " $MANAGED $answered " != *" $k "* ]] && custom+="$line"$'\n'
  done < "$ETC/server.env"
fi
for k in $answered; do
  [[ -z "${!k}" ]] || custom+="$k=\"${!k}\""$'\n' # empty = back to the default
done
env_backup=""
if [[ -f $ETC/server.env ]]; then
  env_backup="$ETC/server.env.bak"
  cp -p "$ETC/server.env" "$env_backup"
fi
write "$ETC/server.env" root:lightspeak 0640 <<EOF
# Lightspeak server – written by install.sh (secrets are kept on re-run).
# Own settings below the marker survive a re-run of the installer.
LS_PUBLIC_URL=https://$LS_PUBLIC_HOST
LS_LIVEKIT_URL=wss://$LS_RTC_HOST
LS_LIVEKIT_API_URL=http://127.0.0.1:7880
LS_ADDR=127.0.0.1:8080
LS_WEBHOOK_ADDR=127.0.0.1:8081
LS_DATA_DIR=$DATA
LS_SERVER_NAME="$LS_SERVER_NAME"
LS_SIGNUP=$LS_SIGNUP
LS_TRUST_PROXY=1
LS_MAX_UPLOAD_MB=$LS_MAX_UPLOAD_MB
LS_STORAGE_QUOTA_GB=$LS_STORAGE_QUOTA_GB
LS_MAX_VOICE_PARTICIPANTS=$LS_MAX_VOICE_PARTICIPANTS
LIVEKIT_API_KEY=$LK_KEY
LIVEKIT_API_SECRET=$LK_SECRET
LS_FILE_URL_SECRET=$FILE_SECRET
# --- own settings ---
# Check with: sudo lightspeak-admin config
$custom
# Available, shown with their defaults (all explained in server.env.example):
#   LS_INVITES=members            members or admins: who may create space invites
#   LS_MAX_SPACES=3               spaces per regular account (0 = admins only)
#   LS_MAX_MESSAGE_CHARS=4000     100-20000
#   LS_MAX_SPACE_MEMBERS=500      2-1000
#   LS_MAX_SPACE_CHANNELS=200     10-1000
#   LS_MAX_SPACE_ROLES=25         2-100
#   LS_RETENTION_DAYS=0           delete attachments after N days (0 = keep)
EOF
# Refuse a broken configuration while the old server still runs.
candidate=$APP_DIR/current/lightspeak-server
[[ ! -x $STAGE/lightspeak-server ]] || candidate=$STAGE/lightspeak-server
if [[ -x $candidate ]] && ! check_config "$candidate"; then
  [[ -z "$env_backup" ]] || mv -f "$env_backup" "$ETC/server.env"
  die "Invalid settings, see above. Nothing was restarted."
fi
rm -f "$ETC/server.env.bak"
write "$ETC/livekit.env" root:livekit 0640 <<EOF
LIVEKIT_KEYS="$LK_KEY: $LK_SECRET"
EOF
# A public address on the outgoing interface (typical VPS) needs no STUN
# lookup at start; behind NAT LiveKit asks a public STUN server.
src_ip=$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
LK_EXTERNAL_IP=true
if [[ -n "$src_ip" && ! "$src_ip" =~ ^(10\.|127\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.|169\.254\.) ]]; then
  LK_EXTERNAL_IP=false
fi
write "$ETC/livekit.yaml" root:livekit 0640 <<EOF
# LiveKit for Lightspeak – written by install.sh. Keys come from livekit.env.
port: 7880
bind_addresses: ["127.0.0.1"]   # signalling only via Caddy (wss://$LS_RTC_HOST)
logging:
  level: info
rtc:
  tcp_port: 7881                # ICE/TCP fallback (public)
  udp_port: 7882                # single UDP port for all media (public)
  use_external_ip: $LK_EXTERNAL_IP   # false: the public IP is on the interface
  advertise_internal_ip: $LK_EXTERNAL_IP   # behind NAT: LAN clients connect directly, hairpin NAT often fails for UDP
room:
  auto_create: true
  empty_timeout: 300
  departure_timeout: 20
  max_participants: $LS_MAX_VOICE_PARTICIPANTS
webhook:
  api_key: $LK_KEY
  urls:
    - http://127.0.0.1:8081/livekit/webhook
turn:
  enabled: false
EOF

# ---------- Caddy ----------
{
  echo "# Lightspeak – written by install.sh"
  [[ -n "$LS_ACME_EMAIL" ]] && printf '# ACME e-mail is set in the global options of /etc/caddy/Caddyfile if needed.\n'
  cat <<EOF
$LS_PUBLIC_HOST {
	encode zstd gzip
	header {
		Strict-Transport-Security "max-age=31536000"
		X-Content-Type-Options "nosniff"
		Referrer-Policy "no-referrer"
		-Server
	}
	@upload path_regexp upload ^/api/v1/channels/[0-9]+/attachments\$
	handle @upload {
		request_body {
			max_size $((LS_MAX_UPLOAD_MB + 10))MB
		}
		reverse_proxy 127.0.0.1:8080 {
			flush_interval -1
		}
	}
	handle {
		request_body {
			max_size 8MB
		}
		reverse_proxy 127.0.0.1:8080
	}
}

$LS_RTC_HOST {
	reverse_proxy 127.0.0.1:7880
}
EOF
} | write "$CADDY_SITE" root:root 0644
# The package ships a demo ":80" site; if that file is still unmodified it is
# replaced (backup kept), otherwise our site is only imported.
pkg_md5=$(dpkg-query -W -f='${Conffiles}\n' caddy 2>/dev/null | awk '$1=="/etc/caddy/Caddyfile"{print $2}')
if [[ -f /etc/caddy/Caddyfile && -n "$pkg_md5" && "$(md5sum < /etc/caddy/Caddyfile | cut -d' ' -f1)" == "$pkg_md5" ]]; then
  cp -a /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak-$(date +%Y%m%d%H%M%S)"
  rm -f /etc/caddy/Caddyfile
fi
if [[ ! -f /etc/caddy/Caddyfile ]]; then
  printf '# Caddyfile – sites are imported below.\n' | write /etc/caddy/Caddyfile root:root 0644
fi
if ! grep -qF "import $CADDY_SITE" /etc/caddy/Caddyfile; then
  cp -a /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.bak-$(date +%Y%m%d%H%M%S)"
  if [[ -n "$LS_ACME_EMAIL" ]] && ! grep -qE '^\s*\{' /etc/caddy/Caddyfile; then
    { printf '{\n\temail %s\n}\n\n' "$LS_ACME_EMAIL"; cat /etc/caddy/Caddyfile; } > /etc/caddy/Caddyfile.new
    mv -f /etc/caddy/Caddyfile.new /etc/caddy/Caddyfile
  fi
  printf '\nimport %s\n' "$CADDY_SITE" >> /etc/caddy/Caddyfile
fi
if ! out=$(caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile 2>&1); then
  printf '%s\n' "$out" | tail -n 5 >&2
  die "Caddy rejected the configuration."
fi

# ---------- admin helper ----------
[[ ! -f "$0" || "$0" -ef "$INSTALLER_COPY" ]] || install -m 0755 -o root -g root "$0" "$INSTALLER_COPY"
write "$ADMIN_BIN" root:root 0755 <<'EOF'
#!/bin/sh
# Lightspeak admin commands (bootstrap, create-invite, reset-password, …).
# Usage: sudo lightspeak-admin <command> [options]
#   update [--version x.y.z]  newest signed release, with backup and rollback
#   config                    check the settings and show them (no secrets)
#   reconfigure               apply server.env to Caddy, LiveKit and the services
set -e
[ "$(id -u)" -eq 0 ] || { echo "Run with sudo." >&2; exit 1; }
case "${1-}" in
  update) shift; exec bash /usr/local/lib/lightspeak/install.sh --update "$@" ;;
  reconfigure) exec bash /usr/local/lib/lightspeak/install.sh --yes ;;
esac
set -a; . /etc/lightspeak/server.env; set +a
if [ "${1-}" = config ]; then
  exec runuser -u lightspeak -- /opt/lightspeak/current/lightspeak-server check-config
fi
exec runuser -u lightspeak -- /opt/lightspeak/current/lightspeak-server admin "$@"
EOF

# ---------- systemd ----------
say "Services"
write "$UNIT_DIR/lightspeak-server.service" root:root 0644 <<EOF
[Unit]
Description=Lightspeak server (API, WebSocket, files)
After=network-online.target lightspeak-livekit.service
Wants=network-online.target
ConditionPathExists=$APP_DIR/current/lightspeak-server

[Service]
Type=simple
User=lightspeak
Group=lightspeak
EnvironmentFile=$ETC/server.env
ExecStart=$APP_DIR/current/lightspeak-server serve
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
CapabilityBoundingSet=
AmbientCapabilities=
ReadWritePaths=$DATA
UMask=0027
MemoryMax=384M
LimitNOFILE=8192

[Install]
WantedBy=multi-user.target
EOF
write "$UNIT_DIR/lightspeak-livekit.service" root:root 0644 <<EOF
[Unit]
Description=LiveKit SFU for Lightspeak
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=livekit
Group=livekit
EnvironmentFile=$ETC/livekit.env
ExecStart=$lk_ver_dir/livekit-server --config $ETC/livekit.yaml
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
LockPersonality=yes
SystemCallArchitectures=native
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
CapabilityBoundingSet=
UMask=0027
MemoryMax=512M
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
write "$UNIT_DIR/lightspeak-backup.service" root:root 0644 <<EOF
[Unit]
Description=Lightspeak database backup
ConditionPathExists=$APP_DIR/current/lightspeak-server

[Service]
Type=oneshot
User=lightspeak
Group=lightspeak
EnvironmentFile=$ETC/server.env
ExecStart=$APP_DIR/current/lightspeak-server admin backup --keep 3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=$DATA
UMask=0027
EOF
write "$UNIT_DIR/lightspeak-backup.timer" root:root 0644 <<'EOF'
[Unit]
Description=Daily Lightspeak database backup

[Timer]
OnCalendar=*-*-* 04:30:00
RandomizedDelaySec=15m
Persistent=true

[Install]
WantedBy=timers.target
EOF
# Caddy's admin API listens on localhost:2019 without authentication: any
# local process could rewrite the proxy config and read the TLS traffic.
# Move it to a unix socket in a directory only the caddy user can enter
# (`caddy reload` reads the same CADDY_ADMIN variable).
CADDY_DROPIN=/etc/systemd/system/caddy.service.d/lightspeak-admin.conf
caddy_admin_new=0
[[ -f $CADDY_DROPIN ]] || caddy_admin_new=1
install -d -m 0755 /etc/systemd/system/caddy.service.d
write "$CADDY_DROPIN" root:root 0644 <<'EOF'
# Lightspeak: admin API only via a socket reachable by the caddy user (install.sh)
[Service]
RuntimeDirectory=caddy
RuntimeDirectoryMode=0750
Environment=CADDY_ADMIN=unix//run/caddy/admin.sock
EOF
systemctl daemon-reload
systemctl enable lightspeak-livekit.service >/dev/null 2>&1
systemctl restart lightspeak-livekit.service
systemctl enable lightspeak-server.service lightspeak-backup.timer >/dev/null 2>&1
systemctl start lightspeak-backup.timer
if [[ -d $STAGE ]]; then
  activate_release
elif [[ -e "$APP_DIR/current/lightspeak-server" ]]; then
  restart_server
fi
systemctl enable caddy >/dev/null 2>&1 || true
if [[ $caddy_admin_new == 1 ]]; then
  systemctl restart caddy # the admin address changes: a reload cannot reach it
else
  systemctl reload caddy 2>/dev/null || systemctl restart caddy
fi
if ! systemctl is-active --quiet caddy; then
  warn "Caddy did not start with the admin socket – reverting to its default admin address."
  rm -f "$CADDY_DROPIN"; systemctl daemon-reload; systemctl restart caddy
fi

# ---------- deploy permissions ----------
if [[ -n "$LS_DEPLOY_USER" ]]; then
  u=$LS_DEPLOY_USER
  sc=$(command -v systemctl); jc=$(command -v journalctl)
  sud=$(mktemp)
  cat > "$sud" <<EOF
# Lightspeak: let $u deploy without a password (written by install.sh)
$u ALL=(root) NOPASSWD: $sc restart lightspeak-server, $sc status lightspeak-server, $sc restart lightspeak-livekit, $sc status lightspeak-livekit, $sc reload caddy, $jc -u lightspeak-server -n 200 --no-pager, $jc -u lightspeak-livekit -n 200 --no-pager, $jc -u caddy -n 200 --no-pager, $ADMIN_BIN
EOF
  visudo -cf "$sud" >/dev/null || die "sudoers validation failed."
  install -m 0440 -o root -g root "$sud" /etc/sudoers.d/lightspeak; rm -f "$sud"
else
  rm -f /etc/sudoers.d/lightspeak
fi

# ---------- firewall ----------
if [[ $ufw_state != none ]]; then
  : "${LS_FIREWALL:=}"
  if [[ -z "$LS_FIREWALL" ]]; then confirm "Add ufw rules for 80,443/tcp, 443/udp (HTTP/3), 7881/tcp, 7882/udp (SSH stays allowed)?" && LS_FIREWALL=yes || LS_FIREWALL=no; fi
  if [[ $LS_FIREWALL == yes ]]; then
    ufw allow OpenSSH >/dev/null || ufw allow 22/tcp >/dev/null
    for r in 80/tcp 443/tcp 443/udp 7881/tcp 7882/udp; do ufw allow "$r" >/dev/null; done
    if [[ $ufw_state == inactive ]]; then
      # Enabling a firewall can lock you out; never implied by --yes.
      if [[ ${LS_UFW_ENABLE-} == yes ]] || { [[ $ASSUME_YES == 0 ]] && confirm "ufw is inactive. Enable it now? (SSH stays allowed)"; }; then
        ufw --force enable >/dev/null
      else
        warn "ufw rules added but ufw stays inactive (enable later with: sudo ufw enable)."
      fi
    fi
  fi
fi
warn "If your provider has its own firewall (a cloud firewall or security group), open 80/tcp, 443/tcp, 443/udp, 7881/tcp and 7882/udp there as well."

# ---------- result ----------
echo
say "Status"
for s in lightspeak-livekit lightspeak-server caddy; do printf '  %-20s %s\n' "$s" "$(systemctl is-active "$s" 2>/dev/null || true)"; done
if [[ ! -e "$APP_DIR/current/lightspeak-server" ]]; then
  warn "No server installed yet. Run again with --version latest or --server-binary <file>."
  exit 0
fi
healthy || die "Server did not start: journalctl -u lightspeak-server -n 50"
admins=$(sqlite3 -readonly "$DATA/data/lightspeak.db" 'SELECT COUNT(*) FROM users WHERE is_server_admin=1' 2>/dev/null || echo 0)
if [[ "$admins" == 0 ]]; then
  echo
  say "Owner invite – open it on the PC where Lightspeak is installed:"
  "$ADMIN_BIN" bootstrap || true
fi
echo
say "Done. Open https://$LS_PUBLIC_HOST in a browser to check the certificate."
