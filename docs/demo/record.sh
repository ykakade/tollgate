#!/usr/bin/env bash
# Records docs/assets/demo.gif with fake providers: Claude Code sends one
# request through Tollgate, the primary provider fails, Tollgate sends the
# request to the backup, and the dashboard shows the spend.
# Needs vhs, ffmpeg, jq, Claude Code and Chrome or Chromium (set CHROME).
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
bin=$root/bin
chrome=${CHROME:-chromium}
out=$root/docs/assets/demo.gif

for tool in vhs ffmpeg jq claude "$chrome"; do
	command -v "$tool" >/dev/null || { echo "$tool is not installed. Install it and try again." >&2; exit 1; }
done

dir=$(mktemp -d)
pids=()
cleanup() {
	kill "${pids[@]}" 2>/dev/null || true
	wait 2>/dev/null || true
	rm -rf "$dir"
}
trap cleanup EXIT

keys=$("$bin/tollgate" keys create -id laptop)
key=$(awk '/^Key:/ {print $2}' <<<"$keys")
hash=$(awk '/hash:/ {print $2}' <<<"$keys")

cat >"$dir/tollgate.yaml" <<YAML
version: 1
server:
  listen: "127.0.0.1:8080"
  admin_listen: "127.0.0.1:9090"
usage:
  path: $dir/usage.jsonl
providers:
  primary: {kind: anthropic, base_url: "http://127.0.0.1:8091"}
  backup: {kind: anthropic, base_url: "http://127.0.0.1:8092"}
routes:
  - model: claude-sonnet-5-5
    api: anthropic
    targets: [{provider: primary}, {provider: backup}]
  - model: claude-haiku-5-5
    api: anthropic
    targets: [{provider: primary}, {provider: backup}]
tenants:
  - id: personal
    keys: [{id: laptop, hash: "$hash"}]
    models: [claude-sonnet-5-5, claude-haiku-5-5]
    budgets: [{window: month, limit_usd: 50}]
YAML

"$bin/fakeprovider" -listen 127.0.0.1:8091 -scenario status:529 2>/dev/null &
pids+=($!)
"$bin/fakeprovider" -listen 127.0.0.1:8092 -first-token-delay 300ms -token-interval 30ms 2>/dev/null &
pids+=($!)
"$bin/tollgate" serve -config "$dir/tollgate.yaml" 2>"$dir/tollgate.log" &
pids+=($!)
for _ in $(seq 50); do
	curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1 && break
	sleep 0.1
done
curl -fsS http://127.0.0.1:8080/readyz >/dev/null

# The tape inherits these variables. A new Claude Code configuration
# directory keeps the recording free of the local login and settings.
export TOLLGATE_DEMO_DIR=$dir
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN=$key
export ANTHROPIC_MODEL=claude-sonnet-5-5
export ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-5-5
export ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-5-5
export CLAUDE_CONFIG_DIR=$dir/claude
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

(cd "$dir" && vhs -q "$root/docs/demo/demo.tape")

"$chrome" --headless --disable-gpu --hide-scrollbars --force-dark-mode \
	--blink-settings=preferredColorScheme=0 --window-size=1200,640 \
	--virtual-time-budget=5000 --screenshot="$dir/dashboard.png" \
	http://127.0.0.1:9090/ 2>/dev/null

# Show the dashboard for 5 seconds after the terminal part.
ffmpeg -loglevel error -y -i "$dir/demo-terminal.gif" -loop 1 -t 5 -i "$dir/dashboard.png" -filter_complex \
	"[0:v]fps=25,scale=1200:640,setsar=1[a];[1:v]fps=25,scale=1200:640,setsar=1[b];[a][b]concat=n=2:v=1,split[x][y];[x]palettegen=stats_mode=diff[p];[y][p]paletteuse=dither=bayer:bayer_scale=4" \
	"$out"
echo "Wrote $out"
