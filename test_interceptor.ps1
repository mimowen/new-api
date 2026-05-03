$token = "sk-XENzRnIqeYEApfilOmJCYN5rxQ4bnQN6pQ0VHmYtR7wgi6QN"
$baseUrl = "http://localhost:3000"

function Write-TestResult($testName, $success, $message) {
    if ($success) {
        Write-Host "[PASS] $testName" -ForegroundColor Green
    } else {
        Write-Host "[FAIL] $testName" -ForegroundColor Red
    }
    Write-Host "       $message" -ForegroundColor Gray
}

$headers = @{
    "Authorization" = "Bearer $token"
    "Content-Type" = "application/json"
}

Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "   Model Interceptor Test Suite" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan

Write-Host "=== Test 1: Intercept 'default' model ===" -ForegroundColor Yellow
$body = @{
    model = "default"
    messages = @(@{ role = "user"; content = "Say hello" })
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
    $intercepted = $response.model -ne "default"
    Write-TestResult "Test 1" $intercepted "Request model: 'default' -> Response model: '$($response.model)'"
} catch {
    Write-TestResult "Test 1" $false "Request failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 2: Pass through normal model ===" -ForegroundColor Yellow
$normalModel = "meta/llama-4-maverick-17b-128e-instruct"
$body = @{
    model = $normalModel
    messages = @(@{ role = "user"; content = "Say hi" })
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
    $passedThrough = $response.model -eq $normalModel
    Write-TestResult "Test 2" $passedThrough "Request model: '$normalModel' -> Response model: '$($response.model)'"
} catch {
    Write-TestResult "Test 2" $false "Request failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 3: Rank status API ===" -ForegroundColor Yellow
try {
    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $hasDefault = $status.data.default -ne $null
    $hasModels = $status.data.default.models.Count -gt 0
    Write-TestResult "Test 3" ($hasDefault -and $hasModels) "Default category has $($status.data.default.models.Count) models"
} catch {
    Write-TestResult "Test 3" $false "API failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 4: Rank page HTML ===" -ForegroundColor Yellow
try {
    $html = Invoke-WebRequest -Uri "$baseUrl/api/model_rank" -Method Get -UseBasicParsing
    $hasHtml = $html.Content -match "<!DOCTYPE html>" -or $html.Content -match "<html"
    Write-TestResult "Test 4" $hasHtml "Page returned $($html.Content.Length) bytes"
} catch {
    Write-TestResult "Test 4" $false "Page failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 5: Multiple requests (rank change) ===" -ForegroundColor Yellow
$body = @{
    model = "default"
    messages = @(@{ role = "user"; content = "Test" })
} | ConvertTo-Json -Depth 3

$successCount = 0
for ($i = 1; $i -le 3; $i++) {
    try {
        $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
        if ($response.model -ne "default") { $successCount++ }
    } catch {}
}
Write-TestResult "Test 5" ($successCount -eq 3) "3 requests intercepted: $successCount/3"

Write-Host "`n=== Test 6: Check rank scores changed ===" -ForegroundColor Yellow
Start-Sleep -Milliseconds 500
try {
    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $topModel = $status.data.default.models[0]
    $scoreChanged = $topModel.score -ne 40
    Write-TestResult "Test 6" $scoreChanged "Top model '$($topModel.model)' score: $($topModel.score) (initial: 40)"
} catch {
    Write-TestResult "Test 6" $false "API failed: $($_.Exception.Message)"
}

Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "   Test Complete" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan
