# Browser Engine Fallback

## Overview

The browser engine fallback feature provides automatic recovery from anti-bot protection responses (403, 429, 503 with challenge pages) by retrying failed requests through a browser automation engine.

## Configuration

### Feature Toggle

Add to `config.json`:

```json
{
  "features": {
    "browser_engine_fallback": true
  }
}
```

Or set via environment variable:
```bash
QWEN2API_FEATURES_BROWSER_ENGINE_FALLBACK=true
```

**Default:** `false` (disabled)

### Dynamic Configuration

The toggle can be updated at runtime via the admin API:

```bash
curl -X POST http://localhost:5001/admin/config/features \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"browser_engine_fallback": true}'
```

## How It Works

### Anti-Bot Detection

The system detects anti-bot responses using:

1. **Status codes:**
   - `403 Forbidden`
   - `429 Too Many Requests`
   - `503 Service Unavailable` (with HTML content-type)

2. **Body keywords:**
   - "challenge"
   - "captcha"
   - "cloudflare"
   - "verify"

### Fallback Flow

```
1. Try HTTP request
   ↓
2. Check response for anti-bot signals
   ↓
3. If anti-bot detected AND feature enabled AND browser available:
   → Retry via browser engine
   ↓
4. Return browser response OR original error
```

### Affected Endpoints

All upstream Qwen API calls use fallback:
- `/api/models` (model list)
- `/api/v2/chats/new` (chat creation)
- `/api/v2/chat/completions` (streaming completions)

## Architecture

### Components

- **`internal/qwen/client.go`**: Core fallback logic
  - `doWithFallback()`: Non-streaming requests
  - `doStreamWithFallback()`: Streaming requests
  - `isAntiBotResponse()`: Detection logic

- **`internal/browserengine/engine.go`**: Browser abstraction
  - `HybridEngine`: HTTP-first with browser fallback
  - `BrowserClient`: Interface for browser implementations

- **`internal/config/config.go`**: Feature toggle
  - `FeatureToggles.BrowserEngineFallback`

### Integration Points

- `cmd/qwen2api/main.go`: CLI entrypoint
- `api/index.go`: Vercel serverless entrypoint
- Both pass `BrowserFallbackEnabled` to client config

## Testing

### Unit Tests

```bash
# Test fallback behavior
go test ./internal/qwen -v -run Fallback

# Test anti-bot detection
go test ./internal/qwen -v -run AntiBot

# Test config integration
go test ./internal/config -v -run BrowserEngine
```

### Manual Testing

1. **Simulate anti-bot response:**
```bash
# Start test server that returns 403
go run test/antibot_server.go

# Configure qwen2api to use test server
export QWEN2API_BASE_URL=http://localhost:8080
export QWEN2API_FEATURES_BROWSER_ENGINE_FALLBACK=true

# Make request - should recover via fallback
curl http://localhost:5001/v1/models \
  -H "Authorization: Bearer YOUR_API_KEY"
```

2. **Verify dashboard shows toggle:**
```bash
curl http://localhost:5001/dashboard/data \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  | jq '.features.browser_engine_fallback'
```

## Performance Considerations

### When Fallback is Disabled (default)

- **Zero overhead**: No browser engine initialization
- **Fast failures**: Anti-bot responses fail immediately
- **Low memory**: No browser process

### When Fallback is Enabled

- **Lazy initialization**: Browser engine created only when needed
- **First fallback cost**: ~2-5s for browser startup
- **Subsequent fallbacks**: ~1-2s per request
- **Memory overhead**: ~50-100MB for browser process

### Recommendations

- **Enable for production** if you frequently hit anti-bot protection
- **Disable for development** to fail fast and debug issues
- **Monitor metrics** to track fallback usage:
  - Check logs for "browser fallback succeeded" messages
  - Track request latency increases

## Limitations

### Current Implementation

1. **No concrete browser backend yet**: The abstraction is in place, but no actual browser automation library is integrated. Fallback will fail gracefully until a backend (Playwright, Rod, etc.) is added.

2. **No retry limits**: Fallback attempts once. If browser path also fails, the error is returned.

3. **No circuit breaker**: Repeated anti-bot responses don't trigger automatic disabling.

### Future Enhancements

- [ ] Integrate Playwright or Rod for actual browser automation
- [ ] Add retry limits and exponential backoff
- [ ] Implement circuit breaker pattern
- [ ] Add metrics for fallback success/failure rates
- [ ] Support custom browser profiles and fingerprints
- [ ] Add request queuing to avoid browser overload

## Security Considerations

### Browser Automation Risks

- **Resource exhaustion**: Browser processes consume significant memory
- **Code injection**: Malicious responses could exploit browser vulnerabilities
- **Credential leakage**: Browser may cache sensitive data

### Mitigations

- Browser engine runs in isolated process
- No persistent browser state between requests
- Fallback only triggers on specific anti-bot signals
- Feature disabled by default

## Troubleshooting

### Fallback Not Triggering

1. **Check feature is enabled:**
```bash
curl http://localhost:5001/dashboard/data | jq '.features.browser_engine_fallback'
```

2. **Verify anti-bot detection:**
   - Check response status code (403/429/503)
   - Check response body for keywords
   - Review logs for "anti-bot response detected" messages

3. **Confirm browser engine available:**
   - Check logs for "browser engine initialized" message
   - Verify no browser startup errors

### Fallback Failing

1. **Check browser engine logs:**
```bash
grep "browser" qwen2api.log
```

2. **Verify browser dependencies installed:**
   - Playwright: `npx playwright install`
   - Rod: Chrome/Chromium binary in PATH

3. **Test browser engine directly:**
```bash
go test ./internal/browserengine -v
```

### High Latency

1. **Disable fallback if not needed:**
```json
{"features": {"browser_engine_fallback": false}}
```

2. **Monitor fallback frequency:**
   - If >10% of requests use fallback, investigate upstream issues
   - Consider rotating tokens or adjusting rate limits

3. **Optimize browser settings:**
   - Disable images/CSS loading
   - Use headless mode
   - Reduce timeout values

## Related Documentation

- [Feature Toggles](./features.md)
- [Configuration Guide](./configuration.md)
- [Admin API Reference](./admin-api.md)
- [Browser Engine Architecture](./browser-engine.md)
