#!/usr/bin/env python3
import json
with open("/home/ubuntu/qwen2api/config.json") as f:
    c = json.load(f)
c["model_aliases"] = {
    "claude-sonnet-4-20250514": "qwen3.7-max",
    "claude-opus-4-20250514": "qwen3.7-max",
    "claude-3-5-sonnet-20241022": "qwen3.7-max",
    "claude-3-5-haiku-20241022": "qwen3.5-flash",
    "claude-sonnet-4.6": "qwen3.7-max",
    "claude-opus-4.7": "qwen3.7-max"
}
with open("/home/ubuntu/qwen2api/config.json", "w") as f:
    json.dump(c, f, indent=4)
print("OK")
