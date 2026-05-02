$token = "sk-XENzRnIqeYEApfilOmJCYN5rxQ4bnQN6pQ0VHmYtR7wgi6QN"
$baseUrl = "http://localhost:3000"

Write-Host "=== 测试 1：使用 'default' 模型 ===" -ForegroundColor Green
$body = @{
    model = "default"
    messages = @(
        @{
            role = "user"
            content = "Hello, please just reply with one sentence."
        }
    )
} | ConvertTo-Json

$headers = @{
    "Authorization" = "Bearer $token"
    "Content-Type" = "application/json"
}

try {
    $response = Invoke-RestMethod -Uri "$baseUrl/v1/chat/completions" -Method Post -Body $body -Headers $headers
    Write-Host "请求成功！" -ForegroundColor Green
    Write-Host "响应模型: $($response.model)" -ForegroundColor Cyan
    Write-Host "响应内容: $($response.choices[0].message.content)" -ForegroundColor Gray
} catch {
    Write-Host "请求失败: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host "响应内容: $($_.ErrorDetails)" -ForegroundColor Yellow
}

Write-Host "`n=== 查看 Docker 日志确认拦截 ===" -ForegroundColor Magenta
docker logs new-api-local --tail 20
