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
Write-Host "   Model Interceptor E2E Test Suite" -ForegroundColor Cyan
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
    $errorMsg = $_.Exception.Message
    $hasError = $errorMsg -match "503" -or $errorMsg -match "500"
    Write-TestResult "Test 3" $hasError "Got expected error: $errorMsg"
}

Write-Host "`n=== Test 4: Rank status API ===" -ForegroundColor Yellow
try {
    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $hasDefault = $status.data.default -ne $null
    $hasModels = $status.data.default.models.Count -gt 0
    $hasConfig = $status.config -ne $null
    Write-TestResult "Test 4" ($hasDefault -and $hasModels -and $hasConfig) "Default category has $($status.data.default.models.Count) models, config present: $hasConfig"
} catch {
    Write-TestResult "Test 4" $false "API failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 5: Rank page HTML ===" -ForegroundColor Yellow
try {
    $html = Invoke-WebRequest -Uri "$baseUrl/api/model_rank" -Method Get -UseBasicParsing
    $hasHtml = $html.Content -match "<!DOCTYPE html>"
    Write-TestResult "Test 5" $hasHtml "Page returned $($html.Content.Length) bytes"
} catch {
    Write-TestResult "Test 5" $false "Page failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 6: Stream response with 'default' model ===" -ForegroundColor Yellow
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
    Write-TestResult "Test 6" $success "Stream response contains model replacement, length: $($content.Length) bytes"
} catch {
    Write-TestResult "Test 6" $false "Request failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 7: Interceptor mode switch (passthrough/intercept) ===" -ForegroundColor Yellow
try {
    $modeStatus = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Get
    $originalEnabled = $modeStatus.data.intercept_enabled

    $newMode = -not $originalEnabled
    $setResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{intercept_enabled=$newMode}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $modeChanged = $setResult.data.intercept_enabled -eq $newMode

    $restoreResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{intercept_enabled=$originalEnabled}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $restored = $restoreResult.data.intercept_enabled -eq $originalEnabled

    Write-TestResult "Test 7" ($modeChanged -and $restored) "Mode switch: $originalEnabled -> $newMode -> $originalEnabled"
} catch {
    Write-TestResult "Test 7" $false "Mode switch failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 8: Add and remove category ===" -ForegroundColor Yellow
try {
    $addResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/category/add" -Method Post -Body (@{category="e2e_test_cat";patterns=@("e2e-test-pattern")}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $added = $addResult.success

    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $found = $status.data.e2e_test_cat -ne $null

    $removeResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/category/remove" -Method Post -Body (@{category="e2e_test_cat"}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $removed = $removeResult.success

    $statusAfter = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $gone = $statusAfter.data.e2e_test_cat -eq $null

    Write-TestResult "Test 8" ($added -and $found -and $removed -and $gone) "Category add/remove: added=$added, found=$found, removed=$removed, gone=$gone"
} catch {
    Write-TestResult "Test 8" $false "Category CRUD failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 9: Add and remove model in category ===" -ForegroundColor Yellow
try {
    Invoke-RestMethod -Uri "$baseUrl/api/model_rank/category/add" -Method Post -Body (@{category="e2e_model_test";patterns=@("e2e-model")}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}

    $addModelResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/model/add" -Method Post -Body (@{category="e2e_model_test";model="e2e-fake-model";weight=1}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $modelAdded = $addModelResult.success

    $status = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/status" -Method Get
    $modelFound = $false
    if ($status.data.e2e_model_test) {
        foreach ($m in $status.data.e2e_model_test.models) {
            if ($m.model -eq "e2e-fake-model") { $modelFound = $true; break }
        }
    }

    $removeModelResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/model/remove" -Method Post -Body (@{category="e2e_model_test";model="e2e-fake-model"}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $modelRemoved = $removeModelResult.success

    Invoke-RestMethod -Uri "$baseUrl/api/model_rank/category/remove" -Method Post -Body (@{category="e2e_model_test"}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}

    Write-TestResult "Test 9" ($modelAdded -and $modelFound -and $modelRemoved) "Model add/remove: added=$modelAdded, found=$modelFound, removed=$modelRemoved"
} catch {
    Write-TestResult "Test 9" $false "Model CRUD failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 10: Save config ===" -ForegroundColor Yellow
try {
    $saveResult = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/config/save" -Method Post
    Write-TestResult "Test 10" $saveResult.success "Save config: $($saveResult.message)"
} catch {
    Write-TestResult "Test 10" $false "Save config failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 11: Capture mode switch ===" -ForegroundColor Yellow
try {
    $enableCapture = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{capture_enabled=$true}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $captureOn = $enableCapture.data.capture_enabled -eq $true

    Start-Sleep -Milliseconds 500

    $disableCapture = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{capture_enabled=$false}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    $captureOff = $disableCapture.data.capture_enabled -eq $false

    Write-TestResult "Test 11" ($captureOn -and $captureOff) "Capture switch: on=$captureOn, off=$captureOff"
} catch {
    Write-TestResult "Test 11" $false "Capture switch failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 12: Capture page HTML ===" -ForegroundColor Yellow
try {
    $html = Invoke-WebRequest -Uri "$baseUrl/api/model_rank/capture" -Method Get -UseBasicParsing
    $hasHtml = $html.Content -match "<!DOCTYPE html>"
    Write-TestResult "Test 12" $hasHtml "Capture page returned $($html.Content.Length) bytes"
} catch {
    Write-TestResult "Test 12" $false "Capture page failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 13: Capture records API ===" -ForegroundColor Yellow
try {
    $enableCapture = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{capture_enabled=$true}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}

    $body = @{
        model = "default"
        messages = @(@{ role = "user"; content = "Test capture" })
    } | ConvertTo-Json -Depth 3
    try { Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers } catch {}

    Start-Sleep -Seconds 2

    $records = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/capture/records?limit=10" -Method Get
    $hasRecords = $records.success -and $records.data.total -ge 0

    Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{capture_enabled=$false}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}

    Write-TestResult "Test 13" $hasRecords "Capture records: total=$($records.data.total)"
} catch {
    Write-TestResult "Test 13" $false "Capture records failed: $($_.Exception.Message)"
}

Write-Host "`n=== Test 14: Passthrough mode - no interception ===" -ForegroundColor Yellow
try {
    $originalMode = Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Get
    $wasEnabled = $originalMode.data.intercept_enabled

    Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{intercept_enabled=$false}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}

    $body = @{
        model = "default"
        messages = @(@{ role = "user"; content = "Hello" })
    } | ConvertTo-Json -Depth 3

    try {
        $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
        $passthroughWorks = $false
    } catch {
        $passthroughWorks = $_.Exception.Message -match "503"
    }

    if ($wasEnabled) {
        Invoke-RestMethod -Uri "$baseUrl/api/model_rank/mode" -Method Post -Body (@{intercept_enabled=$true}|ConvertTo-Json) -Headers @{"Content-Type"="application/json"}
    }

    Write-TestResult "Test 14" $passthroughWorks "Passthrough mode: interceptor disabled, 'default' model not intercepted (expected 503)"
} catch {
    Write-TestResult "Test 14" $false "Passthrough test failed: $($_.Exception.Message)"
}

Write-Host "`n========================================" -ForegroundColor Cyan
Write-Host "   E2E Test Complete" -ForegroundColor Cyan
Write-Host "========================================`n" -ForegroundColor Cyan
