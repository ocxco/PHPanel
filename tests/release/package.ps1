$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$buildRoot=Join-Path $projectRoot '.build'
$testRoot=Join-Path $buildRoot ('package-test-'+[Guid]::NewGuid().ToString('N'))
$binaries=Join-Path $testRoot 'binaries'
$output=Join-Path $testRoot 'output'
New-Item -ItemType Directory -Force -Path $binaries | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem
try {
    # Package fixtures only; never execute a binary or copy a real installation.
    foreach($name in @('PHPanel.exe','phpanel-cli.exe','phpanel-service.exe')) {
        [IO.File]::WriteAllText((Join-Path $binaries $name),'fixture '+$name)
    }
    foreach($directory in @('packages','instances','sites','state','downloads')) {
        $path=Join-Path $binaries $directory
        New-Item -ItemType Directory -Path $path | Out-Null
        [IO.File]::WriteAllText((Join-Path $path 'private.txt'),'must not ship')
    }
    $zip=Join-Path $output 'PHPanel-0.1.0-windows-x64-portable.zip'
    $checksums=Join-Path $output 'PHPanel-0.1.0-windows-x64-SHA256SUMS.txt'
    $previousHash=''
    foreach($round in 1..2) {
        [IO.File]::WriteAllText((Join-Path $binaries 'PHPanel.exe'),('fixture revision '+$round))
        & (Join-Path $projectRoot 'scripts/package.ps1') -Version 0.1.0 -Target Portable -SkipBuild -BinariesDirectory $binaries -OutputDirectory $output | Out-Null
        $actual=(Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
        $lines=@(Get-Content -LiteralPath $checksums)
        if($lines.Count -ne 1 -or $lines[0] -cne ($actual+'  '+[IO.Path]::GetFileName($zip))){throw 'Missing or stale Portable checksum'}
        if($round -eq 2 -and $actual -eq $previousHash){throw 'Changed payload did not change archive hash'}
        $previousHash=$actual
        $archive=[IO.Compression.ZipFile]::OpenRead($zip)
        try {
            $entries=@($archive.Entries | ForEach-Object {$_.FullName.Replace('\','/')})
            if($entries | Where-Object {$_ -match '/(packages|instances|sites|state|downloads)/'}){throw 'Runtime data included in Portable'}
            if($entries | Where-Object {$_ -match '(^|/)docs(/|$)'}){throw 'Documentation directory included in Portable'}
            foreach($name in @('PHPanel.exe','phpanel-cli.exe','phpanel-service.exe','portable.flag','README.md')) {
                if($entries -notcontains ('PHPanel-0.1.0-windows-x64-portable/'+$name)){throw ('Missing payload entry: '+$name)}
            }
            $readmeEntry=$archive.GetEntry('PHPanel-0.1.0-windows-x64-portable/README.md')
            $reader=[IO.StreamReader]::new($readmeEntry.Open())
            try {$readme=$reader.ReadToEnd()} finally {$reader.Dispose()}
            if($readme.Contains('](docs/')){throw 'Packaged README links to excluded documentation'}
            if(-not $readme.Contains('](https://github.com/ocxco/PHPanel/blob/main/docs/')){throw 'Packaged README is missing online documentation links'}
        } finally {$archive.Dispose()}
    }
    Write-Output 'Packaging tests passed: payload isolation, required files, and checksum refresh.'
} finally {
    $resolved=(Resolve-Path -LiteralPath $testRoot).Path
    if(-not $resolved.StartsWith($buildRoot+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Test cleanup outside workspace'}
    if((Get-Item -LiteralPath $resolved -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Test cleanup root is redirected'}
    if(Get-ChildItem -LiteralPath $resolved -Recurse -Force | Where-Object {$_.Attributes -band [IO.FileAttributes]::ReparsePoint}){throw 'Test cleanup contains redirected paths'}
    Remove-Item -LiteralPath $resolved -Recurse -Force
}
