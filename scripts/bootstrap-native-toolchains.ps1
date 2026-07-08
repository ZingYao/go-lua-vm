param(
    [switch]$Install,
    [switch]$EmitEnv,
    [string]$Bash = "bash"
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$scriptPath = Join-Path $repoRoot "scripts/bootstrap-native-toolchains.sh"
$bashArgs = @()

if ($Install) {
    $bashArgs += "--install"
}
if ($EmitEnv) {
    $bashArgs += "--emit-env"
}

& $Bash $scriptPath @bashArgs
exit $LASTEXITCODE
