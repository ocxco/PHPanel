param([string]$Output = 'PHPanel.exe')
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location $projectRoot
try {
    Push-Location frontend
    try { & npm.cmd run build; if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' } } finally { Pop-Location }
    & go build -o build/bin/phpanel-cli.exe ./cmd/phpanel-cli
    if ($LASTEXITCODE -ne 0) { throw 'CLI launcher build failed' }
    & go build -ldflags '-s -w -H=windowsgui' -o build/bin/phpanel-service.exe ./cmd/phpanel-service
    if ($LASTEXITCODE -ne 0) { throw 'System service host build failed' }
    if (Get-Command wails -ErrorAction SilentlyContinue) { & wails build -s -m -nosyncgomod -platform windows/amd64 -o $Output }
    else { & go run github.com/wailsapp/wails/v2/cmd/wails@v2.11.0 build -s -m -nosyncgomod -platform windows/amd64 -o $Output }
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
} finally { Pop-Location }
