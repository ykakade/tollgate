#!/bin/sh
# Sends a mix of demo requests to Tollgate until the container stops.
# Usage: loadgen.sh <workers>
set -u

URL=${TOLLGATE_URL:-http://tollgate:8080}
# The demo key of deploy/compose/tollgate.yaml. It is public: use it only
# with the demo stack.
KEY=tg_enLSL9uN7ccL7D64Rd2VmtWH9eTVTOZ5UTDZqOxC1ST
WORKERS=${1:-4}

# rand prints a number from 0 to 255.
rand() {
	od -An -N1 -tu1 /dev/urandom | tr -d ' '
}

anthropic() { # model stream session
	curl -sS -o /dev/null -w '%{http_code} ' "$URL/v1/messages" \
		-H "x-api-key: $KEY" \
		-H 'anthropic-version: 2023-06-01' \
		-H 'content-type: application/json' \
		-H "x-tollgate-session: $3" \
		-d "{\"model\":\"$1\",\"max_tokens\":64,\"stream\":$2,\"messages\":[{\"role\":\"user\",\"content\":\"Say hello.\"}]}"
}

openai() { # stream
	curl -sS -o /dev/null -w '%{http_code} ' "$URL/v1/chat/completions" \
		-H "authorization: Bearer $KEY" \
		-H 'content-type: application/json' \
		-d "{\"model\":\"gpt-demo\",\"stream\":$1,\"messages\":[{\"role\":\"user\",\"content\":\"Say hello.\"}]}"
}

worker() {
	session="demo-$1"
	while :; do
		r=$(rand)
		if [ "$r" -lt 150 ]; then
			anthropic claude-sonnet-5-5 true "$session"
		elif [ "$r" -lt 200 ]; then
			anthropic claude-haiku-5-5 true "$session"
		elif [ "$r" -lt 225 ]; then
			anthropic claude-sonnet-5-5 false "$session"
		else
			openai true
		fi
		# Wait 0.2 to 1.2 seconds.
		sleep "$(awk -v r="$(rand)" 'BEGIN { printf "%.2f", 0.2 + r / 255 }')"
	done
}

echo "loadgen: sending requests to $URL with $WORKERS workers"
i=1
while [ "$i" -le "$WORKERS" ]; do
	worker "$i" &
	i=$((i + 1))
done
wait
