param(
    [ValidateSet('up', 'down', 'migrate', 'api', 'worker', 'web', 'demo', 'env', 'mail')]
    [string]$Command = 'up'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$goBin = Join-Path $env:LOCALAPPDATA 'Programs\go\bin'
if (Test-Path $goBin) {
    $env:Path = "$goBin;$env:Path"
}

function Import-DotEnv {
    if (-not (Test-Path '.env')) {
        throw '.env not found. Copy .env.example to .env first.'
    }
    Get-Content .env | Where-Object { $_ -match '^\s*[^#].*=' } | ForEach-Object {
        $key, $value = $_ -split '=', 2
        Set-Item -Path "env:$($key.Trim())" -Value $value.Trim()
    }
}

function Start-Mailpit {
    $certDir = Join-Path $root '.local\mailpit'
    if (-not (Test-Path (Join-Path $certDir 'cert.pem'))) {
        throw 'Mailpit certificate missing. See .local/mailpit setup notes.'
    }
    $exists = docker ps -a --filter name=styk-mailpit --format '{{.Names}}'
    if ($exists) {
        docker start styk-mailpit | Out-Null
    } else {
        docker run -d --name styk-mailpit --restart unless-stopped `
            -p 127.0.0.1:1025:1025 -p 127.0.0.1:8025:8025 `
            -v "${certDir}:/certs:ro" axllent/mailpit:latest `
            --smtp-tls-cert /certs/cert.pem --smtp-tls-key /certs/key.pem | Out-Null
    }
    'Mailpit: http://127.0.0.1:8025'
}

switch ($Command) {
    'up' { docker compose up -d --wait }
    'down' { docker compose down }
    'mail' { Start-Mailpit }
    'migrate' {
        Import-DotEnv
        go run -tags='no_mysql no_sqlite3 no_mssql no_ydb no_vertica no_clickhouse no_libsql' github.com/pressly/goose/v3/cmd/goose@v3.25.0 -dir migrations postgres $env:DATABASE_URL up
    }
    'api' {
        Import-DotEnv
        go run ./cmd/api
    }
    'worker' {
        Import-DotEnv
        go run ./cmd/worker
    }
    'web' {
        Push-Location web
        try { npm run dev } finally { Pop-Location }
    }
    'demo' {
        Import-DotEnv
        go run ./cmd/demo
    }
    'env' {
        Import-DotEnv
        Get-ChildItem env: |
            Where-Object { $_.Name -match '^(DATABASE_URL|HTTP_ADDR|STYK_|WORKER_|QUERY_|MOYSKLAD_|OZON_)' } |
            Format-Table -AutoSize
    }
}
