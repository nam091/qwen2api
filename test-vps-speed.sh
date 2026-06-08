#!/bin/bash
# test-vps-speed.sh - Run ON THE VPS to diagnose upstream latency
# Usage: ssh ubuntu@100.110.125.8 'bash -s' < test-vps-speed.sh

echo "=== 1. VPS -> Qwen upstream ==="
curl -o /dev/null -s -w "Connect: %{time_connect}s | TLS: %{time_appconnect}s | FirstByte: %{time_starttransfer}s\n" https://chat.qwen.ai

echo ""
echo "=== 2. Localhost proxy (non-stream) ==="
curl -o /dev/null -s -w "FirstByte: %{time_starttransfer}s | Total: %{time_total}s\n" \
  http://localhost:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.7-max","messages":[{"role":"user","content":"say hi"}],"stream":false}'

echo ""
echo "=== 3. Localhost proxy (stream, first token) ==="
curl -N -o /dev/null -s -w "FirstByte: %{time_starttransfer}s | Total: %{time_total}s\n" \
  http://localhost:5001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3.7-max","messages":[{"role":"user","content":"say hi"}],"stream":true}'

echo ""
echo "=== 4. Reverse proxy check ==="
if command -v nginx &>/dev/null; then
  echo "nginx found:"
  grep -ri "buffering\|proxy_buffer" /etc/nginx/sites-enabled/ /etc/nginx/conf.d/ 2>/dev/null || echo "  (no buffering config found)"
else
  echo "no nginx"
fi
if command -v caddy &>/dev/null; then
  echo "caddy found: $(systemctl is-active caddy 2>/dev/null)"
else
  echo "no caddy"
fi

echo ""
echo "=== 5. VPS location ==="
curl -s ifconfig.me && echo ""

echo ""
echo "=== DONE ==="
echo "Paste these results back to Claude for analysis."
