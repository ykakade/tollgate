#!/bin/sh
# Checks that the running demo stack has live data: Prometheus has request
# metrics from Tollgate, Grafana has the dashboard, and Jaeger has traces.
# Run it after "make demo-up". It waits up to 120 seconds.
set -eu

PROMETHEUS=${PROMETHEUS_URL:-http://127.0.0.1:9091}
GRAFANA=${GRAFANA_URL:-http://127.0.0.1:3000}
JAEGER=${JAEGER_URL:-http://127.0.0.1:16686}

# wait_for runs the command until it succeeds, for up to 120 seconds.
wait_for() { # description command...
	what=$1
	shift
	i=0
	until "$@" >/dev/null 2>&1; do
		i=$((i + 1))
		if [ "$i" -ge 60 ]; then
			echo "check failed: $what" >&2
			return 1
		fi
		sleep 2
	done
	echo "ok: $what"
}

has_series() { # query
	curl -fsS --get "$PROMETHEUS/api/v1/query" --data-urlencode "query=$1" | grep -q '"value"'
}

wait_for "Prometheus has requests from Tollgate" has_series 'sum(tollgate_requests_total{status_class="2xx"}) > 0'
wait_for "Prometheus has a time to first token" has_series 'sum(tollgate_time_to_first_token_seconds_count) > 0'
wait_for "Prometheus has fallbacks" has_series 'sum(tollgate_fallbacks_total) > 0'
wait_for "Prometheus has breaker states" has_series 'tollgate_breaker_state'
wait_for "Grafana has the Tollgate dashboard" sh -c "curl -fsS '$GRAFANA/api/dashboards/uid/tollgate' | grep -q '\"title\":\"Tollgate\"'"
wait_for "Grafana can query Prometheus" sh -c "curl -fsS '$GRAFANA/api/datasources/proxy/uid/prometheus/api/v1/query?query=up' | grep -q '\"status\":\"success\"'"
wait_for "Jaeger has Tollgate traces" sh -c "curl -fsS '$JAEGER/api/v3/services' | grep -q '\"tollgate\"'"
echo "The demo stack has live data."
