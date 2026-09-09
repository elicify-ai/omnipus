#!/bin/bash
# Re-run the whole MSE-replication spike. Ports 19731 (source CDP), 19732 (http),
# 19733 (viewer CDP). Nothing here touches the live gateway on 10994.
set -u
cd "$(dirname "$0")"

node serve.js > out/serve.log 2>&1 &
SERVE=$!
echo "static server pid=$SERVE  (killed on exit)"
trap 'kill $SERVE 2>/dev/null' EXIT
sleep 1

echo; echo "##### 1. DASH test vector (dash.js, uncapped -> ABR runs to 4K) #####"
node record.js "http://127.0.0.1:19732/source-dash.html" 30 out/dash
node analyse.js dash
node replay.js  dash

echo; echo "##### 2. DASH capped to 2500 kbps (for a comparable bandwidth number) #####"
node record.js "http://127.0.0.1:19732/source-dash.html?cap=2500" 30 out/dash720
node analyse.js dash720
node replay.js  dash720

echo; echo "##### 3. YouTube #####"
node record.js "https://www.youtube.com/watch?v=aqz-KE-bpKQ" 35 out/yt
node analyse.js yt
node replay.js  yt
node replay.js  yt --from 30          # mid-stream join
node replay.js  yt --from 30 --badinit # negative control: oldest init

echo; echo "##### 4. MSE-in-Worker (adversarial: main-thread shim is blind) #####"
node record.js "http://127.0.0.1:19732/source-worker.html?run=dash720" 25 out/worker4
node analyse.js worker4
node replay.js  worker4

echo; echo "##### 5. Codec portability / cross-engine byte replay #####"
node portability.js
node crossengine.js dash720   # H.264+AAC  -> chromium/firefox/webkit
node crossengine.js yt        # AV1+Opus   -> webkit rejects
