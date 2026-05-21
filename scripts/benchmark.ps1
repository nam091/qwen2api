param(
    [string]$BaseUrl = "http://127.0.0.1:15001",
    [string]$ApiKey = "sk-mykey",
    [int]$Runs = 5,
    [string]$Model = "qwen3.6-plus"
)

$headers = @{
    "Authorization" = "Bearer $ApiKey"
    "Content-Type"  = "application/json"
}

$bodyObj = @{
    model = $Model
    stream = $false
    messages = @(
        @{ role = "user"; content = "Write a short hello in Vietnamese." }
    )
}
$body = $bodyObj | ConvertTo-Json -Depth 10

$results = @()
for ($i = 1; $i -le $Runs; $i++) {
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    try {
        $resp = Invoke-RestMethod -Method Post -Uri "$BaseUrl/v1/chat/completions" -Headers $headers -Body $body -TimeoutSec 120
        $sw.Stop()
        $content = ""
        if ($resp.choices -and $resp.choices.Count -gt 0 -and $resp.choices[0].message) {
            $content = $resp.choices[0].message.content
        }
        $results += [PSCustomObject]@{
            run = $i
            ms = [math]::Round($sw.Elapsed.TotalMilliseconds, 2)
            ok = $true
            chars = if ($content) { $content.Length } else { 0 }
        }
        Start-Sleep -Milliseconds 200
    }
    catch {
        $sw.Stop()
        $results += [PSCustomObject]@{
            run = $i
            ms = [math]::Round($sw.Elapsed.TotalMilliseconds, 2)
            ok = $false
            chars = 0
        }
    }
}

$ok = $results | Where-Object { $_.ok -eq $true }
if ($ok.Count -gt 0) {
    $avg = ($ok | Measure-Object -Property ms -Average).Average
    $min = ($ok | Measure-Object -Property ms -Minimum).Minimum
    $max = ($ok | Measure-Object -Property ms -Maximum).Maximum
    Write-Output "Benchmark results ($Runs runs):"
    $results | Format-Table -AutoSize | Out-String | Write-Output
    Write-Output ("Summary: avg={0}ms min={1}ms max={2}ms success={3}/{4}" -f ([math]::Round($avg,2)), $min, $max, $ok.Count, $Runs)
} else {
    Write-Output "All benchmark runs failed."
    $results | Format-Table -AutoSize | Out-String | Write-Output
}
