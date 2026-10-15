# WatchTower self-host setup for Windows.
#   powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
#   ... -NoStart       only create .env
#   ... -SkipPrompts   do not ask for Telegram tokens (fill .env later)
param(
    [switch]$NoStart,
    [switch]$SkipPrompts
)

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')
$envPath = Join-Path (Get-Location) '.env'
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

function New-HexSecret {
    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rng.GetBytes($bytes)
    $rng.Dispose()
    return -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

function Set-EnvValue([string[]]$Lines, [string]$Key, [string]$Value) {
    for ($i = 0; $i -lt $Lines.Count; $i++) {
        if ($Lines[$i].StartsWith("$Key=")) { $Lines[$i] = "$Key=$Value" }
    }
    return ,$Lines
}

function Read-Value([string]$Prompt, [bool]$Secret) {
    if ($Secret) {
        $secure = Read-Host -Prompt $Prompt -AsSecureString
        $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
        try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
        finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
    }
    return Read-Host -Prompt $Prompt
}

# Check Docker before writing anything, so a missing Docker does not leave
# a .env behind that makes the next run skip the prompts.
if (-not $NoStart) {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw 'Docker is not installed. Get Docker Desktop: https://docs.docker.com/desktop/setup/install/windows-install/'
    }
    # Through cmd: in Windows PowerShell 5.1, redirecting a native command's
    # stderr turns it into an error record, which $ErrorActionPreference=Stop
    # would throw on before we can check the exit code.
    cmd /c "docker info >nul 2>&1"
    if ($LASTEXITCODE -ne 0) { throw 'Docker Desktop is installed but not running. Start it and run this script again.' }
}

if (Test-Path $envPath) {
    Write-Host '.env already exists; leaving it untouched.'
} else {
    [string[]]$lines = Get-Content '.env.example'
    foreach ($key in 'DB_ROOT_PASSWORD', 'DB_PASSWORD', 'REDIS_PASSWORD', 'JWT_SECRET') {
        $lines = Set-EnvValue $lines $key (New-HexSecret)
    }

    if (-not $SkipPrompts) {
        Write-Host ''
        Write-Host 'Telegram bots: create two with @BotFather (one for asset alerts, one for sentinel).'
        Write-Host 'Press Enter to skip any value and fill it in .env later.'
        $prompts = @(
            @('TELEGRAM_ASSET_BOT_TOKEN', 'Asset bot token', $true),
            @('VITE_ASSET_BOT_USERNAME', 'Asset bot username', $false),
            @('TELEGRAM_SENTINEL_BOT_TOKEN', 'Sentinel bot token', $true),
            @('VITE_SENTINEL_BOT_USERNAME', 'Sentinel bot username', $false),
            @('TWELVE_DATA_API_KEY', 'Twelve Data API key (optional, needed for stock alerts)', $true)
        )
        foreach ($p in $prompts) {
            $value = (Read-Value $p[1] $p[2]).Trim().TrimStart('@')
            if ($value) { $lines = Set-EnvValue $lines $p[0] $value }
        }
    }

    # LF endings and no BOM: docker compose reads .env byte for byte.
    [System.IO.File]::WriteAllText($envPath, (($lines -join "`n") + "`n"), $utf8NoBom)
    Write-Host 'Created .env with fresh random secrets.'
}

if ($NoStart) {
    Write-Host 'Skipping start (-NoStart). Start later with: docker compose up -d --build'
    exit 0
}

Write-Host 'Building and starting WatchTower (first build takes a few minutes)...'
docker compose up -d --build
if ($LASTEXITCODE -ne 0) { throw 'docker compose up failed. Check the output above.' }

$portLine = Get-Content $envPath | Where-Object { $_ -like 'WT_PORT=*' } | Select-Object -First 1
$port = if ($portLine) { $portLine.Substring(8) } else { '8080' }
if (-not $port) { $port = '8080' }
$url = "http://localhost:$port"

Write-Host 'Waiting for the API...'
$healthy = $false
for ($i = 0; $i -lt 90; $i++) {
    try {
        $res = Invoke-WebRequest -UseBasicParsing -Uri "$url/health" -TimeoutSec 5
        if ($res.Content -match '"status":"ok"') { $healthy = $true; break }
    } catch { }
    Start-Sleep -Seconds 2
}
if (-not $healthy) { throw 'API did not become healthy in 3 minutes. Check: docker compose logs api' }

Write-Host ''
Write-Host "WatchTower is running: $url"
Write-Host 'Register an account, then link Telegram on the Telegram Setup page.'
Write-Host 'Stop: docker compose down    Start again: docker compose up -d'
