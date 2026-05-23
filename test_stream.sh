#!/bin/bash
# Non-stream test
curl -s -X POST http://localhost:15001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-mykey" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-6","max_tokens":20,"messages":[{"role":"user","content":"say hi"}]}'
echo ""
echo "---STREAM TEST---"
# Stream test with timeout
timeout 15 curl -s -N -X POST http://localhost:15001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-mykey" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-6","max_tokens":20,"stream":true,"messages":[{"role":"user","content":"say hi"}]}'
echo ""
echo "---DONE---"
