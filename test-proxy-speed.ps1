# test-proxy-speed.ps1 - Run on LOCAL machine to diagnose proxy latency
# Usage: .\test-proxy-speed.ps1 -ProxyUrl "http://100.110.125.8:5001" -ApiKey "sk-xxx"

param(
    [string]$ProxyUrl = "http://100.110.125.8:5001",
    [string]$ApiKey = "",
    [string]$Model = "qwen3.7-max"
)

Write-Host "=== 1. Ping VPS ===" -ForegroundColor Cyan
$ping = Test-Connection -ComputerName ($ProxyUrl -replace 'https?://' -replace ':.*') -Count 4 -Quiet
if ($ping) {
    $pingResult = Test-Connection -ComputerName ($ProxyUrl -replace 'https?://' -replace ':.*') -Count 4
    $avgMs = ($pingResult | Measure-Object -Property ResponseTime -Average).Average
    Write-Host "  Avg RTT: $([math]::Round($avgMs, 1)) ms"
} else {
    Write-Host "  Ping failed or blocked"
}

Write-Host ""
Write-Host "=== 2. HTTP timing to proxy (non-stream) ===" -ForegroundColor Cyan
$body = @{
    model = $Model
    messages = @(@{ role = "user"; content = "say hi in 3 words" })
    stream = $false
} | ConvertTo-Json -Depth 3

$headers = @{ "Content-Type" = "application/json" }
if ($ApiKey) { $headers["Authorization"] = "Bearer $ApiKey" }

try {
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $resp = Invoke-WebRequest -Uri "$ProxyUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers -TimeoutSec 60
    $sw.Stop()
    Write-Host "  Total: $($sw.ElapsedMilliseconds) ms"
    Write-Host "  Status: $($resp.StatusCode)"
} catch {
    Write-Host "  Error: $($_.Exception.Message)" -ForegroundColor Red
}

Write-Host ""
Write-Host "=== 3. Stream first-token timing ===" -ForegroundColor Cyan
$bodyStream = @{
    model = $Model
    messages = @(@{ role = "user"; content = "say hi in 3 words" })
    stream = $true
} | ConvertTo-Json -Depth 3

try {
    $sw2 = [System.Diagnostics.Stopwatch]::StartNew()
    $req = [System.Net.HttpWebRequest]::Create("$ProxyUrl/v1/chat/completions")
    $req.Method = "POST"
    $req.ContentType = "application/json"
    if ($ApiKey) { $req.Headers.Add("Authorization", "Bearer $ApiKey") }
    $req.Timeout = 60000
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($bodyStream)
    $req.ContentLength = $bytes.Length
    $stream = $req.GetRequestStream()
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Close()

    $response = $req.GetResponse()
    $reader = New-Object System.IO.StreamReader($response.GetResponseStream())
    $firstLine = $reader.ReadLine()
    $sw2.Stop()
    Write-Host "  Time to first SSE line: $($sw2.ElapsedMilliseconds) ms"
    Write-Host "  First line: $($firstLine.Substring(0, [Math]::Min(100, $firstLine.Length)))..."
    $reader.Close()
    $response.Close()
} catch {
    Write-Host "  Error: $($_.Exception.Message)" -ForegroundColor Red
}

Write-Host ""
Write-Host "=== DONE ===" -ForegroundColor Green
Write-Host "Paste these results back to Claude for analysis."
