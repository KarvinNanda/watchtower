#!/bin/sh
# WatchTower self-host setup for macOS and Linux.
#   ./scripts/setup.sh             create .env if missing, then start the stack
#   ./scripts/setup.sh --no-start  only create .env
set -eu

NO_START=0
for arg in "$@"; do
  case "$arg" in
    --no-start) NO_START=1 ;;
    -h|--help) echo "Usage: ./scripts/setup.sh [--no-start]"; exit 0 ;;
    *) echo "Unknown option: $arg" >&2; exit 2 ;;
  esac
done

cd "$(dirname "$0")/.."

say() { printf '%s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# 32 random bytes from the OS CSPRNG, as 64 hex characters.
rand_hex() { od -An -N32 -tx1 /dev/urandom | tr -d ' \n'; }

# set_var KEY VALUE: replace the "KEY=" line in .env.
set_var() {
  tmp=$(mktemp)
  awk -v k="$1" -v v="$2" 'index($0, k "=") == 1 { print k "=" v; next } { print }' .env > "$tmp"
  mv "$tmp" .env
}

ask() { # ask KEY PROMPT SECRET(0|1)
  printf '%s: ' "$2"
  if [ "$3" = 1 ]; then stty -echo; fi
  IFS= read -r val || val=""
  if [ "$3" = 1 ]; then stty echo; printf '\n'; fi
  val=$(printf '%s' "$val" | sed 's/^@//')
  if [ -n "$val" ]; then set_var "$1" "$val"; fi
}

if [ -f .env ]; then
  say ".env already exists; leaving it untouched."
else
  umask 077
  cp .env.example .env
  for k in DB_ROOT_PASSWORD DB_PASSWORD REDIS_PASSWORD JWT_SECRET; do
    set_var "$k" "$(rand_hex)"
  done
  say "Created .env with fresh random secrets."

  if [ -t 0 ]; then
    say ""
    say "Telegram bots: create two with @BotFather (one for asset alerts, one for sentinel)."
    say "Press Enter to skip any value and fill it in .env later."
    ask TELEGRAM_ASSET_BOT_TOKEN "Asset bot token" 1
    ask VITE_ASSET_BOT_USERNAME "Asset bot username" 0
    ask TELEGRAM_SENTINEL_BOT_TOKEN "Sentinel bot token" 1
    ask VITE_SENTINEL_BOT_USERNAME "Sentinel bot username" 0
    ask TWELVE_DATA_API_KEY "Twelve Data API key (optional, needed for stock alerts)" 1
  fi
fi

if [ "$NO_START" -eq 1 ]; then
  say "Skipping start (--no-start). Start later with: docker compose up -d --build"
  exit 0
fi

command -v docker >/dev/null 2>&1 || die "Docker is not installed. Get Docker Desktop (macOS) or Docker Engine (Linux): https://docs.docker.com/get-docker/"
docker info >/dev/null 2>&1 || die "Docker is installed but not running. Start Docker Desktop (macOS) or run: sudo systemctl start docker (Linux)"
docker compose version >/dev/null 2>&1 || die "The docker compose plugin is missing: https://docs.docker.com/compose/install/"

say "Building and starting WatchTower (first build takes a few minutes)..."
docker compose up -d --build

port=$(sed -n 's/^WT_PORT=//p' .env)
port=${port:-8080}
url="http://localhost:$port"

if command -v curl >/dev/null 2>&1; then
  say "Waiting for the API..."
  i=0
  until curl -fsS "$url/health" 2>/dev/null | grep -q '"status":"ok"'; do
    i=$((i + 1))
    [ "$i" -ge 90 ] && die "API did not become healthy in 3 minutes. Check: docker compose logs api"
    sleep 2
  done
fi

say ""
say "WatchTower is running: $url"
say "Register an account, then link Telegram on the Telegram Setup page."
say "Stop: docker compose down    Start again: docker compose up -d"
