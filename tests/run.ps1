param(
    [string]$Filter = '',
    [switch]$Integration,
    [string]$Timeout = '90s',
    [switch]$Vet
)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$replace = @{}
foreach ($group in @(@{Directory='app'; Target=''}, @{Directory='panel'; Target='internal/panel'}, @{Directory='release'; Target='internal/release'})) {
    $sourceDir = Join-Path $PSScriptRoot $group.Directory
    if (-not (Test-Path -LiteralPath $sourceDir)) { continue }
    foreach ($file in Get-ChildItem -LiteralPath $sourceDir -File -Filter '*_test.go') {
        $virtualPath = Join-Path (Join-Path $projectRoot $group.Target) $file.Name
        $replace[$virtualPath] = $file.FullName
    }
}
$overlay = Join-Path ([IO.Path]::GetTempPath()) ('phpanel-tests-' + [Guid]::NewGuid().ToString('N') + '.json')
@{Replace=$replace} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $overlay -Encoding utf8
Push-Location $projectRoot
try {
    $arguments = @('test', '-overlay', $overlay, './...', '-count=1', '-timeout', $Timeout)
    if ($Filter) { $arguments += @('-run', $Filter) }
    if ($Integration) { $arguments += @('-tags', 'integration', '-v') }
    & go @arguments
    $testExit = $LASTEXITCODE
    if ($testExit -ne 0) { exit $testExit }
    if ($Vet) { & go vet -overlay $overlay ./...; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE } }
} finally {
    Pop-Location
    Remove-Item -LiteralPath $overlay -ErrorAction SilentlyContinue
}
