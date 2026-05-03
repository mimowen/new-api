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

Write-Host "=== Test 1: Intercept 'default' model and get valid response ===" -ForegroundColor Yellow
$body = @{
    model = "default"
    messages = @(@{ role = "user"; content = "Say hello in one sentence" })
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
    $intercepted = $response.model -ne "default"
    $content = ""
    if ($response.choices -and $response.choices.Count -gt 0 -and $response.choices[0].message.content) {
        $content = $response.choices[0].message.content
    }
    $hasContent = $content.Length -gt 0
    $success = $intercepted -and $hasContent
    $displayContent = $content.Substring(0, [Math]::Min(50, $content.Length))
    Write-TestResult "Test 1" $success "Model: 'default' -> '$($response.model)', Response: '$displayContent...'"
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

Write-Host "`n=== Test 3: Non-existent model returns error ===" -ForegroundColor Yellow
$fakeModel = "non-existent-model-xyz-12345"
$body = @{
    model = $fakeModel
    messages = @(@{ role = "user"; content = "Hello" })
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
    Write-TestResult "Test 3" $false "Should have failed but got response: $($response.model)"
} catch {
    $errorMessage = $_.ErrorDetails.Message | ConvertFrom-Json
    $hasError = $errorMessage.error -ne $null
    Write-TestResult "Test 3" $hasError "Got expected error: $($errorMessage.error.message.Substring(0, [Math]::Min(80, $errorMessage.error.message.Length)))..."
}

Write-Host "`n=== Test 4: Rank status API ===" -ForegroundColor Yellow
try {
    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $hasDefault = $status.data.default -ne $null
    $hasModels = $status.data.default.models.Count -gt 0
    Write-TestResult "Test 4" ($hasDefault -and $hasModels) "Default category has $($status.data.default.models.Count) models"
} catch {
    Write-TestResult "Test 4" $false "API failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 5: Rank page HTML ===" -ForegroundColor Yellow
try {
    $html = Invoke-WebRequest -Uri "$baseUrl/api/model_rank" -Method Get -UseBasicParsing
    $hasHtml = $html.Content -match "<!DOCTYPE html>" -or $html.Content -match "<html"
    Write-TestResult "Test 5" $hasHtml "Page returned $($html.Content.Length) bytes"
} catch {
    Write-TestResult "Test 5" $false "Page failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 6: Multiple requests (rank change) ===" -ForegroundColor Yellow
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
Write-TestResult "Test 6" ($successCount -eq 3) "3 requests intercepted: $successCount/3"

Write-Host "`n=== Test 7: Check rank scores changed ===" -ForegroundColor Yellow
Start-Sleep -Milliseconds 500
try {
    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $topModel = $status.data.default.models[0]
    $scoreChanged = $topModel.score -ne 40
    Write-TestResult "Test 7" $scoreChanged "Top model '$($topModel.model)' score: $($topModel.score) (initial: 40)"
} catch {
    Write-TestResult "Test 7" $false "API failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 8: Stream response with 'default' model ===" -ForegroundColor Yellow
$body = @{
    model = "default"
    messages = @(@{ role = "user"; content = "Say hello" })
    stream = $true
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-WebRequest -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers -UseBasicParsing
    $content = $response.Content
    $hasStreamData = $content -match "data:" -and $content -match "\[DONE\]"
    $hasModelReplace = $content -match "meta/llama-4-maverick-17b-128e-instruct" -or $content -match "MiniMax" -or $content -match "qwen"
    $notDefault = $content -notmatch '"model":"default"'
    $success = $hasStreamData -and $hasModelReplace -and $notDefault
    Write-TestResult "Test 8" $success "Stream response contains model replacement, length: $($content.Length) bytes"
} catch {
    Write-TestResult "Test 8" $false "Request failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 9: Non-existent model with stream returns error (not empty response) ===" -ForegroundColor Yellow
$fakeModel = "non-existent-model-xyz-12345"
$body = @{
    model = $fakeModel
    messages = @(@{ role = "user"; content = "Hello" })
    stream = $true
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-WebRequest -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers -UseBasicParsing
    $content = $response.Content
    $hasError = $content -match "error" -or $content -match "No available channel"
    $notEmpty = $content.Length -gt 0
    $success = $hasError -and $notEmpty
    if ($success) {
        Write-TestResult "Test 9" $true "Got error response (not empty), length: $($content.Length) bytes"
    } else {
        Write-TestResult "Test 9" $false "Response: $($content.Substring(0, [Math]::Min(100, $content.Length)))"
    }
} catch {
    $errorMsg = $_.Exception.Message
    $hasStatusCode = $errorMsg -match "503" -or $errorMsg -match "500"
    Write-TestResult "Test 9" $hasStatusCode "Got expected HTTP error: $errorMsg"
}

Write-Host "`n=== Test 10: Stream response has valid SSE format ===" -ForegroundColor Yellow
$body = @{
    model = "default"
    messages = @(@{ role = "user"; content = "Hi" })
    stream = $true
} | ConvertTo-Json -Depth 3

try {
    $response = Invoke-WebRequest -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers -UseBasicParsing
    $content = $response.Content
    $hasContentType = $response.Headers."Content-Type" -match "text/event-stream" -or $response.Headers."Content-Type" -match "application/json"
    $hasDone = $content -match "\[DONE\]"
    $hasData = $content -match "data:"
    $success = $hasData -and $hasDone
    Write-TestResult "Test 10" $success "SSE format valid: data chunks present, [DONE] present, Content-Type: $($response.Headers.'Content-Type')"
} catch {
    Write-TestResult "Test 10" $false "Request failed: $($_.Exception.Message)"
}

Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "   Test Complete" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan
