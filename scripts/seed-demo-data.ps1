# ============================================================
# seed-demo-data.ps1  —  populates myPresence with demo data
# Fully self-contained: works on a fresh container with only
# the built-in admin/admin account.
# Run: pwsh -File scripts\seed-demo-data.ps1
# ============================================================

$Base = "http://localhost:8080"

# ── Login ─────────────────────────────────────────────────────────────────────
Write-Host "Logging in..."
$wr = [System.Net.WebRequest]::Create("$Base/login")
$wr.Method = "POST"; $wr.ContentType = "application/x-www-form-urlencoded"; $wr.AllowAutoRedirect = $false
$bd = [System.Text.Encoding]::UTF8.GetBytes("username=admin&password=admin")
$wr.ContentLength = $bd.Length; $st = $wr.GetRequestStream(); $st.Write($bd,0,$bd.Length); $st.Close()
$re = $wr.GetResponse()
$sc = (($re.Headers["Set-Cookie"]) -split ";")[0]
$re.Close()
Write-Host "  Session: $($sc.Substring(0,30))..."

$jh = @{ Cookie = $sc; "Content-Type" = "application/json" }

# ── Helpers ───────────────────────────────────────────────────────────────────
function PostJSON ($url, $obj) {
    $body = $obj | ConvertTo-Json -Compress -Depth 10
    try   { return Invoke-RestMethod $url -Method POST -Headers $jh -Body $body }
    catch { $m = $_.ErrorDetails.Message; if (-not $m) { $m = $_.Exception.Message }; Write-Warning "  POST $url -> $m"; return $null }
}
function PutJSON ($url, $obj) {
    $body = $obj | ConvertTo-Json -Compress -Depth 10
    try   { Invoke-RestMethod $url -Method PUT -Headers $jh -Body $body | Out-Null }
    catch { Write-Warning "  PUT $url -> $($_.Exception.Message)" }
}
function SetPresences ([int]$uid, [string[]]$dates, [int]$statusId) {
    if (-not $dates -or $dates.Count -eq 0) { return }
    PostJSON "$Base/api/presences" @{ user_id=$uid; dates=@($dates); status_id=$statusId; half="full" } | Out-Null
}
function Get-Weekdays ($year, $month) {
    $d = [DateTime]::new($year, $month, 1); $days = @()
    while ($d.Month -eq $month) {
        if ($d.DayOfWeek -notin 'Saturday','Sunday') { $days += $d.ToString("yyyy-MM-dd") }
        $d = $d.AddDays(1)
    }
    return $days
}

# ── Status IDs (auto-seeded at startup) ──────────────────────────────────────
$SITE=1; $REMOTE=2; $TRIP=3; $LEAVE=4; $SICK=5; $TRAINING=6

# ── 1. Create users ───────────────────────────────────────────────────────────
Write-Host "`nCreating users..."
$users = @(
    @{ email="alice.martin@corp.local";  name="Alice Martin";  password="demo1234"; role="team_manager" },
    @{ email="bob.dupont@corp.local";    name="Bob Dupont";    password="demo1234"; role="team_leader" },
    @{ email="claire.leroy@corp.local";  name="Claire Leroy";  password="demo1234"; role="basic" },
    @{ email="david.simon@corp.local";   name="David Simon";   password="demo1234"; role="basic" },
    @{ email="emma.garcia@corp.local";   name="Emma Garcia";   password="demo1234"; role="team_leader,activity_viewer" },
    @{ email="felix.nguyen@corp.local";  name="Felix Nguyen";  password="demo1234"; role="basic" },
    @{ email="grace.chen@corp.local";    name="Grace Chen";    password="demo1234"; role="basic" },
    @{ email="hugo.moreau@corp.local";   name="Hugo Moreau";   password="demo1234"; role="basic" },
    @{ email="iris.blanc@corp.local";    name="Iris Blanc";    password="demo1234"; role="basic" },
    @{ email="julien.roux@corp.local";   name="Julien Roux";   password="demo1234"; role="basic" }
)
$userIDs = @{}
$userIDs["admin"] = 1
foreach ($u in $users) {
    $r = PostJSON "$Base/admin/users" @{ email=$u.email; name=$u.name; password=$u.password }
    if ($r -and $r.id) {
        $userIDs[$u.email] = [int]$r.id
        Write-Host "  '$($u.name)' id=$($r.id)"
        # Set roles (skip if basic — that's the default)
        if ($u.role -and $u.role -ne "basic") {
            $roleArray = @($u.role -split "," | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" })
            PutJSON "$Base/api/users/$($r.id)/roles" @{ roles=$roleArray }
        }
    } else {
        Write-Warning "  Failed to create '$($u.name)' (may already exist)"
    }
}
# Resolve IDs for users that already existed
$existingUsers = Invoke-RestMethod "$Base/api/users" -Headers $jh -ErrorAction SilentlyContinue
if ($existingUsers) {
    foreach ($eu in $existingUsers) {
        if (-not $userIDs[$eu.email]) {
            $userIDs[$eu.email] = [int]$eu.id
            Write-Host "  '$($eu.name)' id=$($eu.id) (existing)"
        }
    }
}

# Shorthand IDs for the fixed demo cast
$U = @{
    admin  = $userIDs["admin"]
    alice  = if ($userIDs["alice.martin@corp.local"]) { $userIDs["alice.martin@corp.local"] } else { 2 }
    bob    = if ($userIDs["bob.dupont@corp.local"])   { $userIDs["bob.dupont@corp.local"] }   else { 3 }
    claire = if ($userIDs["claire.leroy@corp.local"]) { $userIDs["claire.leroy@corp.local"] } else { 4 }
    david  = if ($userIDs["david.simon@corp.local"])  { $userIDs["david.simon@corp.local"] }  else { 5 }
    emma   = if ($userIDs["emma.garcia@corp.local"])  { $userIDs["emma.garcia@corp.local"] }  else { 6 }
    felix  = if ($userIDs["felix.nguyen@corp.local"]) { $userIDs["felix.nguyen@corp.local"] } else { 7 }
    grace  = if ($userIDs["grace.chen@corp.local"])   { $userIDs["grace.chen@corp.local"] }   else { 8 }
    hugo   = if ($userIDs["hugo.moreau@corp.local"])  { $userIDs["hugo.moreau@corp.local"] }  else { 9 }
    iris   = if ($userIDs["iris.blanc@corp.local"])   { $userIDs["iris.blanc@corp.local"] }   else { 10 }
    julien = if ($userIDs["julien.roux@corp.local"])  { $userIDs["julien.roux@corp.local"] }  else { 11 }
}
Write-Host "  User IDs: admin=$($U.admin), alice=$($U.alice), bob=$($U.bob) ..."

# ── 2. Create holidays (French 2026) ─────────────────────────────────────────
Write-Host "`nCreating public holidays (France 2026)..."
$holidays = @(
    @{ date="2026-01-01"; name="New Year's Day";         allow_imputed=$false },
    @{ date="2026-04-06"; name="Easter Monday";          allow_imputed=$false },
    @{ date="2026-05-01"; name="Labour Day";             allow_imputed=$false },
    @{ date="2026-05-08"; name="Victory in Europe Day";  allow_imputed=$false },
    @{ date="2026-05-14"; name="Ascension Day";          allow_imputed=$false },
    @{ date="2026-05-25"; name="Whit Monday";            allow_imputed=$false },
    @{ date="2026-07-14"; name="Bastille Day";           allow_imputed=$false },
    @{ date="2026-08-15"; name="Assumption Day";         allow_imputed=$false },
    @{ date="2026-11-01"; name="All Saints' Day";        allow_imputed=$false },
    @{ date="2026-11-11"; name="Armistice Day";          allow_imputed=$false },
    @{ date="2026-12-25"; name="Christmas Day";          allow_imputed=$false }
)
foreach ($h in $holidays) {
    $r = PostJSON "$Base/admin/holidays" $h
    if ($r -and $r.id) { Write-Host "  '$($h.name)' id=$($r.id)" }
    else                { Write-Warning "  '$($h.name)' may already exist" }
}

# ── 3. Create floorplan + seats ───────────────────────────────────────────────
Write-Host "`nCreating floorplan..."
$fp = PostJSON "$Base/admin/floorplans" @{ name="HQ Open Space" }
if ($fp -and $fp.id) {
    $fpID = [int]$fp.id
    Write-Host "  Floorplan id=$fpID"
    $seats = @(
        @{ label="A1"; x_pct=20.0; y_pct=30.0 },
        @{ label="A2"; x_pct=30.0; y_pct=30.0 },
        @{ label="A3"; x_pct=40.0; y_pct=30.0 },
        @{ label="B1"; x_pct=20.0; y_pct=50.0 },
        @{ label="B2"; x_pct=30.0; y_pct=50.0 },
        @{ label="B3"; x_pct=40.0; y_pct=50.0 },
        @{ label="C1"; x_pct=20.0; y_pct=70.0 },
        @{ label="C2"; x_pct=30.0; y_pct=70.0 }
    )
    $seatIDs = @{}
    foreach ($s in $seats) {
        $sr = PostJSON "$Base/admin/floorplans/$fpID/seats" $s
        if ($sr -and $sr.id) {
            $seatIDs[$s.label] = [int]$sr.id
            Write-Host "  Seat '$($s.label)' id=$($sr.id)"
        }
    }
} else {
    Write-Warning "  Failed to create floorplan (may already exist) — skipping seats"
    # Try to get existing floorplan
    $fpList = Invoke-RestMethod "$Base/api/floorplans" -Headers $jh -ErrorAction SilentlyContinue
    if ($fpList -and $fpList.Count -gt 0) {
        $fpID = [int]$fpList[0].id
        Write-Host "  Using existing floorplan id=$fpID"
        $existSeats = Invoke-RestMethod "$Base/api/seats?floorplan_id=$fpID" -Headers $jh -ErrorAction SilentlyContinue
        $seatIDs = @{}
        if ($existSeats) { foreach ($s in $existSeats) { $seatIDs[$s.label] = [int]$s.id } }
    }
}

# ── 4. Create teams ───────────────────────────────────────────────────────────
Write-Host "`nCreating teams..."
$teamIDs = @{}
foreach ($t in @("Engineering","Marketing","Sales","HR")) {
    $r = PostJSON "$Base/admin/teams" @{name=$t}
    if ($r -and $r.id) {
        $teamIDs[$t] = [int]$r.id
        Write-Host "  '$t' id=$($r.id)"
    } else {
        Write-Warning "  Failed to create team '$t' (may already exist)"
    }
}
# Resolve IDs for any teams that already existed
if ($teamIDs.Count -lt 4) {
    $existing = Invoke-RestMethod "$Base/api/teams" -Headers $jh -ErrorAction SilentlyContinue
    if ($existing) {
        foreach ($t in $existing) {
            if (-not $teamIDs[$t.name] -and (@("Engineering","Marketing","Sales","HR") -contains $t.name)) {
                $teamIDs[$t.name] = [int]$t.id
                Write-Host "  '$($t.name)' id=$($t.id) (existing)"
            }
        }
    }
}

# ── 5. Team members ───────────────────────────────────────────────────────────
Write-Host "`nAdding team members..."
$memberships = @(
    @{ team="Engineering"; keys=@("admin","alice","bob","claire","david","felix") },
    @{ team="Marketing";   keys=@("emma","grace","hugo") },
    @{ team="Sales";       keys=@("iris","julien","bob") },
    @{ team="HR";          keys=@("admin","alice","emma") }
)
foreach ($m in $memberships) {
    $tid = $teamIDs[$m.team]
    if (-not $tid) { Write-Warning "  Team '$($m.team)' not found"; continue }
    foreach ($key in $m.keys) {
        $uid = $U[$key]
        if ($uid) { PostJSON "$Base/admin/teams/$tid/members" @{user_id=[int]$uid} | Out-Null }
    }
    Write-Host "  $($m.team) (id=$tid): $($m.keys -join ',')"
}

# ── 6. Presences: March and May (all users — on-site except Wed=remote) ───────
Write-Host "`nSeeding March and May presences..."
$allUIDs = @($U.admin, $U.alice, $U.bob, $U.claire, $U.david, $U.emma, $U.felix, $U.grace, $U.hugo, $U.iris, $U.julien)
$marchDays = Get-Weekdays 2026 3
$mayDays   = Get-Weekdays 2026 5

foreach ($uid in $allUIDs) {
    SetPresences $uid @($marchDays | Where-Object { ([DateTime]::Parse($_)).DayOfWeek -ne 'Wednesday' }) $SITE
    SetPresences $uid @($marchDays | Where-Object { ([DateTime]::Parse($_)).DayOfWeek -eq 'Wednesday' }) $REMOTE
    SetPresences $uid @($mayDays   | Where-Object { ([DateTime]::Parse($_)).DayOfWeek -ne 'Wednesday' }) $SITE
    SetPresences $uid @($mayDays   | Where-Object { ([DateTime]::Parse($_)).DayOfWeek -eq 'Wednesday' }) $REMOTE
}
Write-Host "  March + May done for $($allUIDs.Count) users"

# ── 7. Presences: April 2026 (unique per-user patterns) ──────────────────────
# April weekdays: 1-3, 7-10, 13-17, 20-24, 27-30  (Apr 6 = Easter Monday, skip)
Write-Host "`nSeeding April 2026 presences..."

SetPresences $U.admin  @("2026-04-01","2026-04-02","2026-04-03","2026-04-08","2026-04-09","2026-04-13","2026-04-14","2026-04-17","2026-04-20","2026-04-22","2026-04-23","2026-04-24","2026-04-27","2026-04-28","2026-04-30") $SITE
SetPresences $U.admin  @("2026-04-07","2026-04-10","2026-04-15","2026-04-16","2026-04-21","2026-04-29") $REMOTE
Write-Host "  admin"

SetPresences $U.alice  @("2026-04-01","2026-04-02","2026-04-08","2026-04-09","2026-04-10","2026-04-14","2026-04-15","2026-04-16","2026-04-20","2026-04-21","2026-04-22","2026-04-27","2026-04-28","2026-04-29") $SITE
SetPresences $U.alice  @("2026-04-03","2026-04-07","2026-04-13","2026-04-17","2026-04-23","2026-04-24") $REMOTE
SetPresences $U.alice  @("2026-04-30") $LEAVE
Write-Host "  Alice"

SetPresences $U.bob    @("2026-04-01","2026-04-03","2026-04-07","2026-04-08","2026-04-10","2026-04-13","2026-04-15","2026-04-16","2026-04-20","2026-04-22","2026-04-23","2026-04-27","2026-04-29") $SITE
SetPresences $U.bob    @("2026-04-02","2026-04-09","2026-04-14","2026-04-17","2026-04-21","2026-04-24","2026-04-28","2026-04-30") $REMOTE
Write-Host "  Bob"

SetPresences $U.claire @("2026-04-01","2026-04-02","2026-04-03","2026-04-07","2026-04-08","2026-04-13","2026-04-14","2026-04-16","2026-04-20","2026-04-21","2026-04-22","2026-04-27","2026-04-28") $SITE
SetPresences $U.claire @("2026-04-09","2026-04-10","2026-04-15","2026-04-17","2026-04-23","2026-04-24","2026-04-29","2026-04-30") $REMOTE
Write-Host "  Claire"

SetPresences $U.david  @("2026-04-02","2026-04-03","2026-04-08","2026-04-09","2026-04-14","2026-04-15","2026-04-16","2026-04-21","2026-04-22","2026-04-23","2026-04-28","2026-04-29","2026-04-30") $SITE
SetPresences $U.david  @("2026-04-01","2026-04-07","2026-04-10","2026-04-13","2026-04-17","2026-04-20","2026-04-24","2026-04-27") $REMOTE
Write-Host "  David"

SetPresences $U.emma   @("2026-04-01","2026-04-02","2026-04-07","2026-04-08","2026-04-09","2026-04-13","2026-04-14","2026-04-15","2026-04-20","2026-04-21","2026-04-27","2026-04-28","2026-04-29") $SITE
SetPresences $U.emma   @("2026-04-03","2026-04-10","2026-04-16","2026-04-22","2026-04-23","2026-04-24","2026-04-30") $REMOTE
SetPresences $U.emma   @("2026-04-17") $TRIP
Write-Host "  Emma"

SetPresences $U.felix  @("2026-04-01","2026-04-03","2026-04-08","2026-04-10","2026-04-14","2026-04-16","2026-04-20","2026-04-22","2026-04-24","2026-04-27","2026-04-29") $SITE
SetPresences $U.felix  @("2026-04-02","2026-04-09","2026-04-13","2026-04-15","2026-04-17","2026-04-21","2026-04-23","2026-04-28","2026-04-30") $REMOTE
SetPresences $U.felix  @("2026-04-07") $SICK
Write-Host "  Felix"

SetPresences $U.grace  @("2026-04-07","2026-04-08","2026-04-09","2026-04-10","2026-04-13","2026-04-15","2026-04-16","2026-04-17","2026-04-20","2026-04-22","2026-04-23","2026-04-27","2026-04-28","2026-04-29","2026-04-30") $SITE
SetPresences $U.grace  @("2026-04-01","2026-04-02","2026-04-03","2026-04-14","2026-04-21","2026-04-24") $REMOTE
Write-Host "  Grace"

SetPresences $U.hugo   @("2026-04-01","2026-04-02","2026-04-09","2026-04-10","2026-04-13","2026-04-14","2026-04-17","2026-04-20","2026-04-21","2026-04-22","2026-04-27","2026-04-28") $SITE
SetPresences $U.hugo   @("2026-04-03","2026-04-07","2026-04-08","2026-04-15","2026-04-16","2026-04-23","2026-04-29","2026-04-30") $REMOTE
SetPresences $U.hugo   @("2026-04-24") $LEAVE
Write-Host "  Hugo"

SetPresences $U.iris   @("2026-04-02","2026-04-03","2026-04-07","2026-04-09","2026-04-14","2026-04-15","2026-04-21","2026-04-22","2026-04-23","2026-04-28","2026-04-29","2026-04-30") $SITE
SetPresences $U.iris   @("2026-04-01","2026-04-08","2026-04-10","2026-04-13","2026-04-16","2026-04-17","2026-04-20","2026-04-24","2026-04-27") $REMOTE
Write-Host "  Iris"

SetPresences $U.julien @("2026-04-01","2026-04-03","2026-04-08","2026-04-13","2026-04-16","2026-04-17","2026-04-20","2026-04-21","2026-04-27","2026-04-28","2026-04-29") $SITE
SetPresences $U.julien @("2026-04-02","2026-04-10","2026-04-14","2026-04-15","2026-04-22","2026-04-23","2026-04-24","2026-04-30") $REMOTE
SetPresences $U.julien @("2026-04-09") $SICK
SetPresences $U.julien @("2026-04-07") $TRAINING
Write-Host "  Julien"
Write-Host "  April 2026 done"

# ── 8. Seat reservations (admin on seat A1) ───────────────────────────────────
$seatA1 = if ($seatIDs -and $seatIDs["A1"]) { $seatIDs["A1"] } else { 1 }
Write-Host "`nBooking seat reservations for admin (seat A1, id=$seatA1)..."
$adminSiteDays = @("2026-04-01","2026-04-02","2026-04-03","2026-04-08","2026-04-09","2026-04-13","2026-04-14","2026-04-17","2026-04-20","2026-04-22")
$r = PostJSON "$Base/api/reservations/bulk" @{seat_id=$seatA1; dates=$adminSiteDays; half="full"}
if ($r) { Write-Host "  Seat A1 reserved: $($r.booked) days" }

Write-Host "`nSeed complete!"
