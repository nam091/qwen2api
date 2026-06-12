# Qwen Available Models (2026-05-22)

## Qwen 3.7 (Latest Flagship)

| Model ID | Name | Modality | Context | Thinking | Search |
|----------|------|----------|---------|----------|--------|
| qwen3.7-max | Qwen3.7-Max | text | 1,000,000 | yes | yes |
| qwen3.7-plus | Qwen3.7-Plus | text, image, video, audio | 1,000,000 | yes (auto) | yes |

## Qwen 3.6

| Model ID | Name | Modality | Context | Thinking | Search |
|----------|------|----------|---------|----------|--------|
| qwen3.6-plus | Qwen3.6-Plus | text, image, video, audio | 1,000,000 | yes (auto) | yes |
| qwen3.6-max-preview | Qwen3.6-Max-Preview | text | 262,144 | yes | no |
| qwen3.6-27b | Qwen3.6-27B | text, image, video | 262,144 | yes | no |
| qwen3.6-35b-a3b | Qwen3.6-35B-A3B | text, image, video, audio | 262,144 | yes (auto) | yes |
| qwen3.6-plus-preview | Qwen3.6-Plus-Preview | text | 1,000,000 | yes | no |

## Qwen 3.5

| Model ID | Name | Modality | Context | Thinking | Search |
|----------|------|----------|---------|----------|--------|
| qwen3.5-plus | Qwen3.5-Plus | text, image, video, audio | 1,000,000 | yes (auto) | yes |
| qwen3.5-flash | Qwen3.5-Flash | text, image, video, audio | 1,000,000 | yes | yes |
| qwen3.5-max-2026-03-08 | Qwen3.5-Max-Preview | text | 262,144 | yes | no |
| qwen3.5-omni-plus | Qwen3.5-Omni-Plus | text, image, video, audio | 262,144 | no | no |
| qwen3.5-omni-flash | Qwen3.5-Omni-Flash | text, image, video, audio | 262,144 | no | no |
| qwen3.5-397b-a17b | Qwen3.5-397B-A17B | text, image, video, audio | 262,144 | yes | yes |
| qwen3.5-122b-a10b | Qwen3.5-122B-A10B | text, image, video, audio | 262,144 | yes | yes |
| qwen3.5-27b | Qwen3.5-27B | text, image, video, audio | 262,144 | yes | yes |
| qwen3.5-35b-a3b | Qwen3.5-35B-A3B | text, image, video, audio | 262,144 | yes | yes |

## Qwen 3

| Model ID | Name | Modality | Context | Thinking | Search |
|----------|------|----------|---------|----------|--------|
| qwen3-max-2026-01-23 | Qwen3-Max | text | 262,144 | yes | yes |
| qwen3-coder-plus | Qwen3-Coder | text | 1,048,576 | no | no |
| qwen3-vl-plus | Qwen3-VL-235B-A22B | text, image, video | 262,144 | yes | no |
| qwen3-omni-flash-2025-12-01 | Qwen3-Omni-Flash | text, image, video, audio | 65,536 | yes | no |
| qwen-plus-2025-07-28 | Qwen3-235B-A22B-2507 | text | 131,072 | yes | no |

## Qwen 2.5

| Model ID | Name | Modality | Context | Thinking | Search |
|----------|------|----------|---------|----------|--------|
| qwen-max-latest | Qwen2.5-Max | text | 131,072 | yes | no |

---

Total: 19 models

Notes:
- "Model ID" is what you pass in the `model` field of the API request
- Thinking mode: append `-thinking` to model ID or set `enable_thinking: true`
- Search mode: append `-search` to model ID
- Auto thinking: model decides when to think (no need to explicitly enable)
