$ErrorActionPreference = "Stop"

$Repo = if ($env:RAPH_REPO) { $env:RAPH_REPO } else { "tesh254/raph" }
$Version = if ($env:RAPH_VERSION) { $env:RAPH_VERSION } else { "latest" }
$BinDir = if ($env:RAPH_BIN_DIR) { $env:RAPH_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "raph\bin" }

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if ($Version -eq "latest") {
    $Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $Tag = $Release.tag_name
} else {
    $Tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
}

$Asset = "raph_windows_$Arch.zip"
$BaseUrl = "https://github.com/$Repo/releases/download/$Tag"
$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) "raph-$([guid]::NewGuid())"
$Archive = Join-Path $TempDir $Asset
$Checksums = Join-Path $TempDir "checksums.txt"

try {
    New-Item -ItemType Directory -Force -Path $TempDir, $BinDir | Out-Null
    Invoke-WebRequest "$BaseUrl/$Asset" -OutFile $Archive
    Invoke-WebRequest "$BaseUrl/checksums.txt" -OutFile $Checksums

    $Expected = ((Get-Content $Checksums | Where-Object { $_ -match "\s\*?$([regex]::Escape($Asset))$" }) -split "\s+")[0]
    if (-not $Expected) { throw "Checksum for $Asset not found" }
    $Actual = (Get-FileHash $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected.ToLowerInvariant()) { throw "Checksum mismatch for $Asset" }

    Expand-Archive $Archive -DestinationPath $TempDir -Force
    $Binary = Get-ChildItem $TempDir -Recurse -Filter "raph.exe" | Select-Object -First 1
    if (-not $Binary) { throw "raph.exe not found in release archive" }
    Copy-Item $Binary.FullName (Join-Path $BinDir "raph.exe") -Force

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($UserPath -split ";") -notcontains $BinDir) {
        [Environment]::SetEnvironmentVariable("Path", (($UserPath.TrimEnd(";") + ";" + $BinDir).TrimStart(";")), "User")
        $env:Path += ";$BinDir"
        Write-Host "Added $BinDir to user PATH. Open a new terminal to use it."
    }
    Write-Host "Installed raph $Tag to $BinDir\raph.exe"
} finally {
    Remove-Item $TempDir -Recurse -Force -ErrorAction SilentlyContinue
}
