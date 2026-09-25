$ErrorActionPreference = "Stop"

$GatewayBase = "http://localhost:8080"
$MockBase = "http://localhost:8081"
$Fabs = @("FAB_A", "FAB_B", "FAB_C", "FAB_D", "FAB_E", "FAB_F", "FAB_G", "FAB_H", "FAB_I", "FAB_J")

function Assert-Equal {
    param(
        [Parameter(Mandatory)] $Actual,
        [Parameter(Mandatory)] $Expected,
        [Parameter(Mandatory)] [string] $Message
    )

    if ($Actual -ne $Expected) {
        throw "$Message. Expected '$Expected', got '$Actual'."
    }
}

function Get-AllFabSettings {
    Invoke-RestMethod -Method Get -Uri "$GatewayBase/api/allfabs-settings"
}

function Set-FabMode {
    param(
        [Parameter(Mandatory)] [string] $Fab,
        [Parameter(Mandatory)] [string] $Mode
    )

    Invoke-RestMethod -Method Put -Uri "$MockBase/admin/fabs/${Fab}?mode=$Mode" | Out-Null
}

try {
    Invoke-RestMethod -Method Post -Uri "$MockBase/admin/reset" | Out-Null

    $response = Get-AllFabSettings
    Assert-Equal $response.status "success" "All-success status mismatch"
    Assert-Equal $response.summary.success 10 "All-success count mismatch"
    Assert-Equal $response.summary.failed 0 "All-success failed count mismatch"
    Write-Host "PASS: all 10 fabs succeeded"

    Set-FabMode -Fab "FAB_J" -Mode "error"
    $response = Get-AllFabSettings
    Assert-Equal $response.status "partial_success" "Partial-success status mismatch"
    Assert-Equal $response.summary.success 9 "Partial-success count mismatch"
    Assert-Equal $response.summary.failed 1 "Partial-success failed count mismatch"
    Write-Host "PASS: one fab returned HTTP 500"

    Set-FabMode -Fab "FAB_J" -Mode "timeout"
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    $response = Get-AllFabSettings
    $stopwatch.Stop()
    Assert-Equal $response.status "partial_success" "Timeout status mismatch"
    Assert-Equal $response.summary.success 9 "Timeout success count mismatch"
    Assert-Equal $response.summary.failed 1 "Timeout failed count mismatch"
    if ($stopwatch.Elapsed.TotalSeconds -lt 29 -or $stopwatch.Elapsed.TotalSeconds -gt 35) {
        throw "Backend timeout was expected around 30 seconds, got $($stopwatch.Elapsed.TotalSeconds) seconds."
    }
    Write-Host "PASS: one fab timed out after $([math]::Round($stopwatch.Elapsed.TotalSeconds, 1)) seconds"

    foreach ($fab in $Fabs) {
        Set-FabMode -Fab $fab -Mode "error"
    }
    $response = Get-AllFabSettings
    Assert-Equal $response.status "failed" "All-failed status mismatch"
    Assert-Equal $response.summary.success 0 "All-failed success count mismatch"
    Assert-Equal $response.summary.failed 10 "All-failed count mismatch"
    Write-Host "PASS: all 10 fabs failed"

    Write-Host "All smoke tests passed."
}
finally {
    Invoke-RestMethod -Method Post -Uri "$MockBase/admin/reset" | Out-Null
}
