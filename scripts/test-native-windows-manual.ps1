param(
    [string]$GoArch = "",
    [string]$Bash = "",
    [switch]$StrictRuntime
)

$ErrorActionPreference = "Stop"

function Resolve-RepoRoot {
    $scriptPath = $PSCommandPath
    if (-not $scriptPath) {
        $scriptPath = $MyInvocation.MyCommand.Path
    }
    return (Resolve-Path (Join-Path (Split-Path -Parent $scriptPath) "..")).Path
}

function Require-Command {
    param(
        [string]$Name,
        [string]$Hint
    )
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if (-not $command) {
        throw "$Name not found. $Hint"
    }
    return $command.Source
}

function Invoke-Step {
    param(
        [string]$Label,
        [scriptblock]$Body
    )
    Write-Host ""
    Write-Host "== $Label =="
    & $Body
}

function Invoke-Bash {
    param(
        [string]$Script,
        [string[]]$ExtraEnv = @()
    )
    $envPairs = @(
        "CGO_ENABLED=1",
        "TARGET_GOOS=windows",
        "TARGET_GOARCH=$script:TargetArch"
    ) + $ExtraEnv

    $prefix = ""
    if ($envPairs.Count -gt 0) {
        $prefix = ($envPairs -join " ") + " "
    }

    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $script:BashPath -lc "cd `"$script:RepoRootForBash`" && ${prefix}./$Script" 2>&1
        $exitCode = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    $text = ($output | Out-String).TrimEnd()
    if ($text.Length -gt 0) {
        Write-Host $text
    }
    if ($exitCode -ne 0) {
        throw "$Script failed with exit code $exitCode"
    }
}

$script:RepoRoot = Resolve-RepoRoot
Set-Location $script:RepoRoot

$goPath = Require-Command "go" "Install Go 1.26.4 and ensure it is first on PATH."
$goVersion = (& $goPath version)
if ($goVersion -notmatch "go1\.26\.4") {
    throw "go version mismatch: expected go1.26.4, got: $goVersion"
}

if ([string]::IsNullOrWhiteSpace($GoArch)) {
    $GoArch = (& $goPath env GOARCH).Trim()
}
$script:TargetArch = $GoArch

if ([string]::IsNullOrWhiteSpace($Bash)) {
    $bashCommand = Get-Command "bash" -ErrorAction SilentlyContinue
    if (-not $bashCommand) {
        throw "bash not found. Install Git for Windows or MSYS2 so the repository bash scripts can run."
    }
    $script:BashPath = $bashCommand.Source
} else {
    if (-not (Test-Path $Bash)) {
        throw "Bash path not found: $Bash"
    }
    $script:BashPath = (Resolve-Path $Bash).Path
}

$script:RepoRootForBash = $script:RepoRoot -replace "\\", "/"

Write-Host "native Windows manual acceptance"
Write-Host "repo_root=$script:RepoRoot"
Write-Host "go=$goVersion"
Write-Host "GOARCH=$script:TargetArch"
Write-Host "bash=$script:BashPath"
Write-Host "NATIVE_CC_WINDOWS_$($script:TargetArch.ToUpper())=$([Environment]::GetEnvironmentVariable("NATIVE_CC_WINDOWS_$($script:TargetArch.ToUpper())"))"
Write-Host "CC=$env:CC"
Write-Host "LUA53_IMPORT_LIB=$env:LUA53_IMPORT_LIB"
Write-Host "NATIVE_WINDOWS_IMPORT_TOOL=$env:NATIVE_WINDOWS_IMPORT_TOOL"
Write-Host "NATIVE_WINDOWS_IMPORT_TOOL_KIND=$env:NATIVE_WINDOWS_IMPORT_TOOL_KIND"

Invoke-Step "default no-CGO Go tests" {
    $env:CGO_ENABLED = "0"
    & $goPath test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "CGO_ENABLED=0 go test ./... failed"
    }
}

Invoke-Step "default gate script" {
    & $script:BashPath -lc "cd `"$script:RepoRootForBash`" && ./scripts/check-go-gates.sh"
    if ($LASTEXITCODE -ne 0) {
        throw "check-go-gates.sh failed"
    }
}

Invoke-Step "Native CGO Go tests" {
    $env:CGO_ENABLED = "1"
    & $goPath test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "CGO_ENABLED=1 go test ./... failed"
    }
}

Invoke-Step "Windows lua53.def drift check" {
    Invoke-Bash "scripts/check-native-windows-def.sh"
}

Invoke-Step "Windows lua53 import library build" {
    Invoke-Bash "scripts/build-native-windows-lua53-importlib.sh"
}

Invoke-Step "Windows fixture DLL build" {
    Invoke-Bash "scripts/build-native-fixtures.sh"
}

Invoke-Step "Windows lua-cjson DLL build" {
    Invoke-Bash "scripts/build-native-cjson.sh"
}

Invoke-Step "Windows LPeg DLL build" {
    Invoke-Bash "scripts/build-native-lpeg.sh"
}

Invoke-Step "Windows LuaSocket DLL build" {
    Invoke-Bash "scripts/build-native-luasocket.sh"
}

Invoke-Step "Windows source build strict aggregate" {
    Invoke-Bash "scripts/check-native-source-builds.sh" -ExtraEnv @("NATIVE_SOURCE_BUILD_TARGETS=windows/$($script:TargetArch)", "NATIVE_SOURCE_REQUIRE_ALL=1")
}

$runtimeScripts = @(
    "scripts/test-native-modules.sh",
    "scripts/test-native-cjson.sh",
    "scripts/test-native-lpeg.sh",
    "scripts/test-native-luasocket.sh",
    "scripts/test-native-real-modules.sh"
)

foreach ($runtimeScript in $runtimeScripts) {
    Invoke-Step "runtime attempt: $runtimeScript" {
        $previousErrorActionPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            $output = & $script:BashPath -lc "cd `"$script:RepoRootForBash`" && CGO_ENABLED=1 TARGET_GOOS=windows TARGET_GOARCH=$script:TargetArch ./$runtimeScript" 2>&1
            $exitCode = $LASTEXITCODE
        } finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        $text = ($output | Out-String).TrimEnd()
        if ($text.Length -gt 0) {
            Write-Host $text
        }
        if ($exitCode -ne 0) {
            throw "$runtimeScript failed with exit code $exitCode"
        }
        if ($StrictRuntime -and $text -match "(?m)(^|:\s+)skip:") {
            throw "$runtimeScript skipped runtime acceptance under -StrictRuntime"
        }
    }
}

Write-Host ""
Write-Host "Windows manual acceptance script finished."
Write-Host "Use -StrictRuntime when Windows runtime scripts must pass instead of reporting skip."
