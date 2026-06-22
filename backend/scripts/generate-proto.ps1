$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$toolRoot = Join-Path $root '.tools\protoc'
$protoc = Join-Path $toolRoot 'bin\protoc.exe'
$goBin = go env GOPATH
$pluginBin = Join-Path $goBin 'bin'

if (-not (Test-Path $protoc)) {
    throw "protoc not found at $protoc"
}

if (-not (Test-Path (Join-Path $pluginBin 'protoc-gen-go.exe'))) {
    throw "protoc-gen-go.exe not found at $pluginBin"
}

$env:PATH = "$pluginBin;$env:PATH"

Push-Location $root
try {
    & $protoc `
        --proto_path=proto `
        --go_out=internal/gen `
        --go_opt=paths=source_relative `
        proto/location/v1/location.proto
}
finally {
    Pop-Location
}
