#!/bin/bash
curl -s -X POST http://localhost:15001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-mykey" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"qwen3.7-max","max_tokens":50,"messages":[{"role":"user","content":"say hi"}]}'
