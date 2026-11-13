param(
    [Parameter(Mandatory=$true)][ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')][string]$Version,
    [ValidateSet('All','Portable','Installer')][string]$Target='All',
    [string]$OutputDirectory='dist',
    [string]$NSISPath='',
    [string]$WebView2InstallerPath='',
    [switch]$SkipBuild,
    [string]$BinariesDirectory='build/bin'
)
$ErrorActionPreference='Stop'
foreach($part in $Version.Split('.')) {
    $component=0
    if(-not [int]::TryParse($part,[ref]$component) -or $component -gt 65535){throw 'Each version component must be between 0 and 65535.'}
}
$projectRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$outputRoot=if([IO.Path]::IsPathRooted($OutputDirectory)){[IO.Path]::GetFullPath($OutputDirectory)}else{[IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDirectory))}
$binaryRoot=if([IO.Path]::IsPathRooted($BinariesDirectory)){[IO.Path]::GetFullPath($BinariesDirectory)}else{[IO.Path]::GetFullPath((Join-Path $projectRoot $BinariesDirectory))}
$runtimeBuildRoot=Join-Path $projectRoot '.build'
New-Item -ItemType Directory -Force -Path $outputRoot,$runtimeBuildRoot | Out-Null
$workRoot=Join-Path $runtimeBuildRoot ('package-'+[Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $workRoot | Out-Null
$restoreGoEnvironment=$false

function Invoke-Checked([string]$Executable,[string[]]$Arguments) {
    & $Executable @Arguments
    if($LASTEXITCODE -ne 0){throw ('Command failed: '+$Executable+' (exit '+$LASTEXITCODE+')')}
}
function Copy-SourceTree([string]$Source,[string]$Destination) {
    if((Get-Item -LiteralPath $Source -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw ('Refusing redirected source: '+$Source)}
    foreach($entry in Get-ChildItem -LiteralPath $Source -Recurse -Force) {
        if($entry.Attributes -band [IO.FileAttributes]::ReparsePoint){throw ('Refusing redirected source: '+$entry.FullName)}
    }
    Copy-Item -LiteralPath $Source -Destination $Destination -Recurse -Force
}
function Remove-WorkDirectory([string]$Path) {
    $resolved=(Resolve-Path -LiteralPath $Path).Path
    if(-not $resolved.StartsWith($runtimeBuildRoot+'\',[StringComparison]::OrdinalIgnoreCase) -or $resolved -eq $runtimeBuildRoot){throw 'Cleanup target is outside the package workspace'}
    if((Get-Item -LiteralPath $resolved -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Cleanup root is redirected'}
    if(Get-ChildItem -LiteralPath $resolved -Recurse -Force | Where-Object {$_.Attributes -band [IO.FileAttributes]::ReparsePoint}){throw 'Cleanup tree contains redirected paths'}
    Remove-Item -LiteralPath $resolved -Recurse -Force
}

Push-Location $projectRoot
try {
    $makensis=$null
    if($Target -ne 'Portable') {
        if($NSISPath){$makensis=(Resolve-Path -LiteralPath $NSISPath).Path}
        else {
            $command=Get-Command makensis -ErrorAction SilentlyContinue
            if($command){$makensis=$command.Source}
            else {foreach($candidate in @((Join-Path $projectRoot '.tools\nsis\Bin\makensis.exe'),'C:\Program Files (x86)\NSIS\makensis.exe','C:\Program Files\NSIS\makensis.exe')){if(Test-Path -LiteralPath $candidate){$makensis=$candidate;break}}}
        }
        if(-not $makensis){throw 'NSIS 3 compiler not found. Install NSIS or pass -NSISPath. To create only Portable, use -Target Portable.'}
    }
    $moduleCache=(& go env GOMODCACHE).Trim()
    if($LASTEXITCODE -ne 0){throw 'Unable to locate Go module cache'}
    $wailsModule=Join-Path $moduleCache 'github.com\wailsapp\wails\v2@v2.11.0'
    $previousGoCache=$env:GOCACHE
    $previousGoTemp=$env:GOTMPDIR
    $restoreGoEnvironment=$true
    $env:GOCACHE=Join-Path $runtimeBuildRoot 'go-cache'
    $env:GOTMPDIR=Join-Path $workRoot 'go-tmp'
    New-Item -ItemType Directory -Force -Path $env:GOCACHE,$env:GOTMPDIR | Out-Null

    if(-not $SkipBuild) {
        Push-Location frontend
        try {Invoke-Checked 'npm.cmd' @('run','build')} finally {Pop-Location}
        $sourceRoot=Join-Path $workRoot 'source'
        New-Item -ItemType Directory -Force -Path $sourceRoot,(Join-Path $sourceRoot 'frontend'),(Join-Path $sourceRoot 'build\windows') | Out-Null
        foreach($file in Get-ChildItem -LiteralPath $projectRoot -File -Filter '*.go'){Copy-Item -LiteralPath $file.FullName -Destination $sourceRoot}
        foreach($name in @('go.mod','go.sum','wails.json')){Copy-Item -LiteralPath (Join-Path $projectRoot $name) -Destination $sourceRoot}
        foreach($name in @('internal','cmd')){Copy-SourceTree (Join-Path $projectRoot $name) (Join-Path $sourceRoot $name)}
        Copy-SourceTree (Join-Path $projectRoot 'frontend\dist') (Join-Path $sourceRoot 'frontend\dist')
        Copy-Item -LiteralPath (Join-Path $projectRoot 'frontend\package.json') -Destination (Join-Path $sourceRoot 'frontend\package.json')
        foreach($name in @('icon.ico','info.json','wails.exe.manifest')){Copy-Item -LiteralPath (Join-Path $projectRoot ('build\windows\'+$name)) -Destination (Join-Path $sourceRoot ('build\windows\'+$name))}
        Copy-Item -LiteralPath (Join-Path $projectRoot 'build\appicon.png') -Destination (Join-Path $sourceRoot 'build\appicon.png')
        $config=Get-Content -LiteralPath (Join-Path $sourceRoot 'wails.json') -Raw | ConvertFrom-Json
        $config | Add-Member -NotePropertyName info -NotePropertyValue @{companyName='PHPanel';productName='PHPanel';productVersion=$Version;copyright='';comments='Windows local development environment'} -Force
        [IO.File]::WriteAllText((Join-Path $sourceRoot 'wails.json'),($config | ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false))
        $wails=Get-Command wails -ErrorAction SilentlyContinue
        if($wails){$wailsExecutable=$wails.Source}
        elseif(Test-Path -LiteralPath $wailsModule){
            $wailsExecutable=Join-Path $workRoot 'wails.exe'
            Push-Location $wailsModule
            try{Invoke-Checked 'go' @('build','-mod=readonly','-o',$wailsExecutable,'./cmd/wails')}finally{Pop-Location}
        }else{throw 'Wails CLI not found. Install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0 first.'}
        Push-Location $sourceRoot
        try {
            New-Item -ItemType Directory -Force -Path 'build/bin' | Out-Null
            Invoke-Checked 'go' @('build','-trimpath','-ldflags','-s -w','-o','build/bin/phpanel-cli.exe','./cmd/phpanel-cli')
            Invoke-Checked 'go' @('build','-trimpath','-ldflags','-s -w -H=windowsgui','-o','build/bin/phpanel-service.exe','./cmd/phpanel-service')
            Invoke-Checked $wailsExecutable @('build','-platform','windows/amd64','-s','-m','-nosyncgomod','-o','PHPanel.exe')
        } finally {Pop-Location}
        $binaryRoot=Join-Path $sourceRoot 'build\bin'
    }

    $payload=Join-Path $workRoot 'payload'
    New-Item -ItemType Directory -Path $payload | Out-Null
    foreach($name in @('PHPanel.exe','phpanel-cli.exe','phpanel-service.exe')) {
        $file=Join-Path $binaryRoot $name
        if(-not (Test-Path -LiteralPath $file -PathType Leaf)){throw ('Missing release binary: '+$file)}
        Copy-Item -LiteralPath $file -Destination $payload
    }
    $readme=Get-Content -LiteralPath README.md -Raw
    $readme=$readme.Replace('](docs/','](https://github.com/ocxco/PHPanel/blob/main/docs/')
    [IO.File]::WriteAllText((Join-Path $payload 'README.md'),$readme,[Text.UTF8Encoding]::new($false))
    $outputs=@()
    $baseName='PHPanel-'+$Version+'-windows-x64'
    if($Target -ne 'Installer') {
        $portable=Join-Path $workRoot ($baseName+'-portable')
        Copy-SourceTree $payload $portable
        New-Item -ItemType File -Path (Join-Path $portable 'portable.flag') | Out-Null
        $zipPath=Join-Path $outputRoot ($baseName+'-portable.zip')
        Compress-Archive -LiteralPath $portable -DestinationPath $zipPath -Force
        $outputs+=$zipPath
    }
    if($Target -ne 'Portable') {
        $guard=Join-Path $workRoot 'package-guard.exe'
        Invoke-Checked 'go' @('build','-trimpath','-ldflags','-s -w','-o',$guard,'./cmd/phpanel-package')
        if($WebView2InstallerPath){$webview=(Resolve-Path -LiteralPath $WebView2InstallerPath).Path}
        else {$webview=Join-Path $wailsModule 'internal\webview2runtime\MicrosoftEdgeWebview2Setup.exe'}
        if(-not (Test-Path -LiteralPath $webview -PathType Leaf)){throw 'WebView2 bootstrapper not found. Pass -WebView2InstallerPath.'}
        $setupPath=Join-Path $outputRoot ($baseName+'-setup.exe')
        Push-Location 'build/windows/installer'
        try {Invoke-Checked $makensis @('/V3','/INPUTCHARSET','UTF8',('/DPRODUCT_VERSION='+$Version),('/DPACKAGE_DIR='+$payload),('/DGUARD_EXE='+$guard),('/DWEBVIEW_INSTALLER='+$webview),('/DOUTPUT_FILE='+$setupPath),'project.nsi')}finally{Pop-Location}
        if(-not (Test-Path -LiteralPath $setupPath -PathType Leaf)){throw 'NSIS did not produce an installer'}
        $outputs+=$setupPath
    }
    $hashLines=@()
    foreach($name in @(($baseName+'-portable.zip'),($baseName+'-setup.exe'))) {
        $file=Join-Path $outputRoot $name
        if(Test-Path -LiteralPath $file -PathType Leaf) {
            $hash=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
            $hashLines+=$hash+'  '+[IO.Path]::GetFileName($file)
        }
    }
    $hashFile=Join-Path $outputRoot ($baseName+'-SHA256SUMS.txt')
    $hashLines | Set-Content -LiteralPath $hashFile -Encoding ascii
    Write-Output 'Release artifacts:'
    $outputs+=$hashFile
    foreach($file in $outputs){Write-Output $file}
} finally {
    if($restoreGoEnvironment){$env:GOCACHE=$previousGoCache;$env:GOTMPDIR=$previousGoTemp}
    Pop-Location
    Remove-WorkDirectory $workRoot
}
