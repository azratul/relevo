#!/usr/bin/env bash
# Reproducible latency measurement: controller -> (emulated WAN) -> relay
# embedded in relevo-host -> simulated backend -> back. ONE_WAY ms are added
# in each direction between controller and relay, like a tunnel hop, so the
# network round trip is 2*ONE_WAY.
# Usage: tools/latency/measure.sh [ONE_WAY_MS=40] [JITTER_MS=10] [COUNT=100]
set -euo pipefail
cd "$(dirname "$0")/../.."
ONE=${1:-40}; JIT=${2:-10}; N=${3:-100}
T=$(mktemp -d); trap 'kill $(jobs -p) 2>/dev/null; wait 2>/dev/null; rm -rf "$T"' EXIT
python3 tools/latency/delayproxy.py 18701 127.0.0.1:18700 "$ONE" "$JIT" &
echo '{"backend":"sim","profile":"presentation","adminListen":"127.0.0.1:0","connection":{"mode":"url","publicUrl":"http://127.0.0.1:18701","localPort":18700,"allowInsecureTunnel":true,"stunServers":[]}}' > "$T/host.json"
./bin/relevo-host --config "$T/host.json" --no-tray --no-browser --start > "$T/host.log" 2>&1 &
for _ in $(seq 50); do grep -qE "Password:|Contraseña:" "$T/host.log" && break; sleep 0.2; done
sleep 1
ID=$(grep -E "^(Link|Enlace):" "$T/host.log" | sed 's#.*/s/##'); PW=$(grep -E "^(Password|Contraseña):" "$T/host.log" | awk '{print $2}')
echo "one-way delay: ${ONE} ms (+0..${JIT} ms jitter); expected network RTT ≈ $((2*ONE)) ms; $N commands at 4/s"
python3 examples/python/relevo_client.py controller --url "http://127.0.0.1:18701/s/$ID" --name lat --password "$PW" \
  --action presentation.next --count "$N" --interval 0.25 --quiet | grep -E "results|round trip"
