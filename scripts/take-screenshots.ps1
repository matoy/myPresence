# Screenshot script for myPresence using Edge CDP
# Usage: pwsh -File scripts\take-screenshots.ps1

$EdgeExe  = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"
$CdpPort  = 9223   # use different port to avoid conflict
$BaseUrl  = "http://localhost:8080"
$OutDir   = Join-Path $PSScriptRoot "..\docs\screenshots"
$TmpDir   = Join-Path $env:TEMP "edge-cdp-screenshots"

# Fresh login to get a valid session cookie
$wr = [System.Net.WebRequest]::Create("$BaseUrl/login")
$wr.Method = "POST"; $wr.ContentType = "application/x-www-form-urlencoded"; $wr.AllowAutoRedirect = $false
$bd = [System.Text.Encoding]::UTF8.GetBytes("username=admin&password=admin")
$wr.ContentLength = $bd.Length; $st = $wr.GetRequestStream(); $st.Write($bd,0,$bd.Length); $st.Close()
$re = $wr.GetResponse(); $Cookie = (($re.Headers["Set-Cookie"]) -split ";")[0]; $re.Close()
Write-Host "Session obtained: $($Cookie.Substring(0,30))..."

New-Item -ItemType Directory -Force $OutDir | Out-Null
New-Item -ItemType Directory -Force $TmpDir | Out-Null

# ── CDP helpers ─────────────────────────────────────────────────────────────

function Start-EdgeCDP {
    $args = "--headless=new","--disable-gpu","--no-sandbox",
             "--window-size=1440,900",
             "--remote-debugging-port=$CdpPort",
             "--user-data-dir=$TmpDir",
             "about:blank"
    $proc = Start-Process $EdgeExe -ArgumentList $args -PassThru
    Start-Sleep -Milliseconds 2500
    return $proc
}

function Get-PageTab {
    $tabs = Invoke-RestMethod "http://localhost:$CdpPort/json" -ErrorAction Stop
    return ($tabs | Where-Object { $_.type -eq "page" } | Select-Object -First 1)
}

function Connect-WS ($url) {
    $ws = [System.Net.WebSockets.ClientWebSocket]::new()
    $ws.ConnectAsync([Uri]$url, [Threading.CancellationToken]::None).GetAwaiter().GetResult() | Out-Null
    return $ws
}

function Send-WS ($ws, $obj) {
    $json = $obj | ConvertTo-Json -Depth 20 -Compress
    $buf  = [System.Text.Encoding]::UTF8.GetBytes($json)
    $seg  = [System.ArraySegment[byte]]::new($buf)
    $ws.SendAsync($seg, [System.Net.WebSockets.WebSocketMessageType]::Text, $true,
                  [Threading.CancellationToken]::None).GetAwaiter().GetResult() | Out-Null
}

function Recv-WS ($ws, [int]$timeoutMs = 8000) {
    $all  = [System.Text.StringBuilder]::new()
    $buf  = [byte[]]::new(1MB)
    $cts  = [System.Threading.CancellationTokenSource]::new($timeoutMs)
    try {
        do {
            $seg    = [System.ArraySegment[byte]]::new($buf)
            $result = $ws.ReceiveAsync($seg, $cts.Token).GetAwaiter().GetResult()
            $all.Append([System.Text.Encoding]::UTF8.GetString($buf, 0, $result.Count)) | Out-Null
        } while (-not $result.EndOfMessage)
    } catch { return $null }
    try { return $all.ToString() | ConvertFrom-Json } catch { return $null }
}

# Send command and wait for matching id response (skip events)
function Invoke-CDP ($ws, [int]$id, [string]$method, $params = @{}) {
    Send-WS $ws @{ id = $id; method = $method; params = $params }
    $deadline = [DateTime]::Now.AddSeconds(30)
    while ([DateTime]::Now -lt $deadline) {
        $msg = Recv-WS $ws 6000
        if ($msg -and $msg.id -eq $id) { return $msg }
    }
    return $null
}

# Wait for a CDP event matching given method
function Wait-CDPEvent ($ws, [string]$method, [int]$timeoutMs = 15000) {
    $deadline = [DateTime]::Now.AddMilliseconds($timeoutMs)
    while ([DateTime]::Now -lt $deadline) {
        $msg = Recv-WS $ws 3000
        if ($msg -and $msg.method -eq $method) { return $msg }
    }
    return $null
}

# ── Screenshot function ───────────────────────────────────────────────────────

function Take-Screenshot ($ws, [int]$cmdId, [string]$url, [string]$filename, [int]$waitMs = 3500) {
    Write-Host "  → $url"

    # Navigate
    $nav = Invoke-CDP $ws $cmdId "Page.navigate" @{ url = $url }
    $cmdId++

    # Wait for load event
    $loaded = Wait-CDPEvent $ws "Page.loadEventFired" 12000
    Start-Sleep -Milliseconds $waitMs   # wait for Alpine + Tailwind CDN to render

    # Capture full-page screenshot
    $scr = Invoke-CDP $ws $cmdId "Page.captureScreenshot" @{
        format  = "png"
        quality = 100
        captureBeyondViewport = $true
        clip = @{ x=0; y=0; width=1440; height=900; scale=1 }
    }
    $cmdId++

    if ($scr -and $scr.result -and $scr.result.data) {
        $path = Join-Path $OutDir "$filename.png"
        [System.IO.File]::WriteAllBytes($path, [System.Convert]::FromBase64String($scr.result.data))
        Write-Host "    ✓ Saved $filename.png"
    } else {
        Write-Host "    ✗ No data for $filename"
    }

    return $cmdId
}

# ── Main ──────────────────────────────────────────────────────────────────────

Write-Host "Starting Edge headless CDP on port $CdpPort…"
$proc = Start-EdgeCDP

try {
    $tab   = Get-PageTab
    if (-not $tab) { throw "No page tab found in CDP" }
    Write-Host "CDP tab: $($tab.id)"

    $ws    = Connect-WS $tab.webSocketDebuggerUrl
    $cmdId = 1

    # Enable domains
    Invoke-CDP $ws $cmdId "Network.enable"  | Out-Null; $cmdId++
    Invoke-CDP $ws $cmdId "Page.enable"     | Out-Null; $cmdId++

    # Set session cookie
    Invoke-CDP $ws $cmdId "Network.setCookie" @{
        name   = "session"
        value  = ($Cookie -replace "^session=","")
        domain = "localhost"
        path   = "/"
        httpOnly = $true
        sameSite = "Lax"
    } | Out-Null; $cmdId++

    # Set language to English
    Invoke-CDP $ws $cmdId "Network.setCookie" @{
        name="lang"; value="en"; domain="localhost"; path="/"; sameSite="Lax"
    } | Out-Null; $cmdId++
    Write-Host "Cookie set"

    # ── Screenshots ───────────────────────────────────────────────────────────
    Write-Host "`nCapturing screenshots…"

    # Helper: restore both cookies before every authenticated screenshot
    $restoreCookies = {
        Invoke-CDP $ws $cmdId "Network.setCookie" @{
            name="session"; value=($Cookie -replace "^session=","")
            domain="localhost"; path="/"; httpOnly=$true; sameSite="Lax"
        } | Out-Null; $cmdId++
        Invoke-CDP $ws $cmdId "Network.setCookie" @{
            name="lang"; value="en"; domain="localhost"; path="/"; sameSite="Lax"
        } | Out-Null; $cmdId++
    }

    # 1. Login page — temporarily clear cookies so user is not logged in
    Invoke-CDP $ws $cmdId "Network.clearBrowserCookies" | Out-Null; $cmdId++
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/login" "01-login" 1500

    # Restore cookies for all further pages
    & $restoreCookies

    # 2. Calendar (current month: April 2026)
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/?year=2026&month=4"  "02-calendar"  4000

    # 3. Status admin
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/admin/statuses"       "03-statuses"  2500

    # 4. Users & Roles
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/admin/users"          "04-users"     2500

    # 5. Teams
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/admin/teams"          "05-teams"     2500

    # 6. Holidays
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/admin/holidays"       "06-holidays"  2500

    # 7. Activity report
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/admin/activity"       "07-activity"  2500

    # 8. Floor plan
    $cmdId = Take-Screenshot $ws $cmdId "$BaseUrl/floorplan"            "08-floorplan" 2500

    $ws.CloseAsync([System.Net.WebSockets.WebSocketCloseStatus]::NormalClosure, "", [Threading.CancellationToken]::None) | Out-Null

    Write-Host ""
    Write-Host "Done! Screenshots saved to: $OutDir"
    Get-ChildItem $OutDir -Filter "*.png" | ForEach-Object { Write-Host "  $($_.Name)  ($([Math]::Round($_.Length/1KB))KB)" }

} finally {
    Start-Sleep -Milliseconds 500
    Stop-Process $proc -Force -ErrorAction SilentlyContinue
    Remove-Item $TmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
