# New API 模型拦截器 - 动态排名系统

## 概述

本次升级为 New API 项目添加了基于动态排名的模型拦截器系统。系统将根据模型的请求成功率，自动调整模型的优先级，提高整体请求成功率。

## 功能特性

### 1. 独立架构设计
- 拦截器作为 `ModelInterceptorHandler` 独立包装路由
- 完全不依赖或修改 `controller/relay.go` 代码
- 内部实现完整的重试循环和响应缓存
- 失败时自动清除渠道上下文，让 relay handler 重新选择

### 2. 动态排名管理
- 每个模型类别独立维护排名队列
- 初始排名按配置文件中定义的权重排列
- 使用读写锁确保线程安全（`sync.RWMutex`）

### 3. 排名调整算法
- 可配置的初始分数、成功奖励、失败惩罚
- **成功请求**：模型得分 +10（默认）
- **失败请求**：模型得分 -20（默认），最低 0
- 每次记录结果后自动重新排序

### 4. 自动重试和故障转移
- 每次请求优先选择当前排名最高的模型
- 当前模型失败时，自动尝试下一个排名的模型
- 支持记录已尝试过的模型，避免重复
- 重试前清除渠道上下文，确保新模型匹配新渠道

### 5. 完整的日志记录
- `[ModelInterceptor]` - 模型拦截相关日志
- `[ModelRanker]` - 模型排名调整相关日志

## 文件修改

### 1. `relay/model_interceptor.go`
- 完全重构，实现 `ModelRanker` 结构体和 `ModelInterceptorHandler`
- 添加 `responseRecorder` 用于缓存响应
- 添加 `clearChannelContext` 用于清除渠道上下文
- 包含所有核心逻辑，完全独立

### 2. `router/relay-router.go`
- 用 `ModelInterceptorHandler` 包装需要拦截的路由
- 移除了全局中间件注册

### 3. `middleware/distributor.go`
- 添加 `mapped_model` context 检查（仅 8 行代码）
- 优先读取拦截器设置的模型名

### 4. `controller/model_rank_api.go` (新增)
- 独立的 API 端点，不污染 relay.go
- `/api/model_rank/status` - 查询当前排名
- `/api/model_rank/add` - 动态添加模型
- `/api/model_rank/remove` - 动态删除模型
- `/model_rank` - Web UI

### 5. `model_mapping.yaml`
- 添加了 `score_config` 配置项
- 配置实际可用的模型

## 配置说明

### `model_mapping.yaml` 完整示例

```yaml
enabled: true
score_config:
  initial_score: 40      # 模型初始分数
  success_bonus: 10      # 成功请求的奖励分数
  failure_penalty: 20    # 失败请求的惩罚分数
mappings:
  default:
    patterns: ["default"]    # 匹配 "default" 模型名
    models:
      - model: "LLM-Research/Llama-4-Maverick-17B-128E-Instruct"
        weight: 20
      - model: "meta/llama-4-maverick-17b-128e-instruct"
        weight: 20
      - model: "MiniMax/MiniMax-M1-80k"
        weight: 20
      - model: "qwen3.5-plus-free"
        weight: 20
      - model: "spark-lite-free"
        weight: 20
  high_tier:
    patterns: []
    models:
      - model: "qwen3.6-plus-free"
        weight: 70
      - model: "kimi-k2.6-free"
        weight: 30
  code:
    patterns: ["code", "coder", "programming", "dev"]
    models:
      - model: "qwen3.6-plus-free"
        weight: 60
      - model: "kimi-k2.6-free"
        weight: 40
  vision:
    patterns: ["vision", "vl", "image", "visual", "gpt-4-vision"]
    models:
      - model: "qwen3.6-plus-free"
        weight: 70
      - model: "kimi-k2.6-free"
        weight: 30
  agent:
    patterns: ["agent", "function", "tool", "assistant"]
    models:
      - model: "qwen3.6-plus-free"
        weight: 80
      - model: "kimi-k2.6-free"
        weight: 20
  gpt:
    patterns: ["gpt-3", "gpt-4", "gpt3", "gpt4"]
    models:
      - model: "qwen3.6-plus-free"
        weight: 75
      - model: "kimi-k2.6-free"
        weight: 25
intercept_endpoints:
  - "/v1/chat/completions"
  - "/v1/completions"
  - "/v1/embeddings"
```

## 系统架构

```
请求 → [Distributor 中间件]
         ↓
    ModelInterceptorHandler (独立包装)
         ↓
    1. 确定模型类别
    2. 获取下一个最佳模型
    3. 替换请求体中的模型名
    4. 设置 mapped_model context
    5. 调用原 Relay handler
    6. 用 responseRecorder 缓存响应
    7. 检查状态码：
       - < 400: 成功，刷新响应，RecordSuccess
       - ≥ 400: 失败，RecordFailure，清除渠道上下文，重试
```

## 测试指南

### 1. 本地启动服务
```bash
cd d:\project\new-api
go run main.go
```

### 2. 使用 PowerShell 测试脚本
```bash
cd d:\project\new-api
.\test_with_token.ps1
```

### 3. 观察日志
启动服务后，观察日志中的关键信息：
```
[ModelInterceptor] Initialized category default with 5 models
[ModelInterceptor] 模型替换: default → xxx
[ModelInterceptor] 模型 xxx 请求成功
[ModelInterceptor] 模型 xxx 请求失败 (status: 404)，尝试下一个
```

## Docker 部署

### 使用 Docker Compose（推荐）

1. 检查 `docker-compose.yml` 是否配置为本地构建：
```yaml
  new-api:
    build:
      context: .
      dockerfile: Dockerfile.dev
    image: new-api:local-build
```

2. 构建并启动：
```bash
cd d:\project\new-api
docker compose up -d --build
```

## 验证步骤

### 1. 检查服务状态
```bash
curl http://localhost:3000/api/status
```

### 2. 测试聊天接口
```bash
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### 3. 查看排名状态
```bash
curl http://localhost:3000/api/model_rank/status
```

### 4. 动态管理（可选）
```bash
# 动态添加模型
curl -X POST http://localhost:3000/api/model_rank/add \
  -H "Content-Type: application/json" \
  -d '{
    "category": "default",
    "model": "new-model",
    "initial_score": 50
  }'

# 动态删除模型
curl -X POST http://localhost:3000/api/model_rank/remove \
  -H "Content-Type: application/json" \
  -d '{
    "category": "default",
    "model": "old-model"
  }'
```

### 5. Web UI
浏览器打开 `http://localhost:3000/model_rank` 可以查看可视化界面（如果配置了）。

## 线程安全

- 使用 `sync.RWMutex` 保证并发安全
- 读取操作用 `RLock`
- 写入操作用 `Lock`

## 兼容性

- 完全向后兼容
- 配置项保持不变
- 启用/禁用开关 `enabled` 依然有效
- 不修改 relay.go，不影响现有功能

## 设计优势

| 特性 | 说明 |
|------|------|
| **独立架构** | 完全不修改 relay.go，易于维护 |
| **自动重试** | 内部实现重试循环，失败时自动切换模型 |
| **渠道重选** | 重试前清除渠道上下文，让新模型匹配正确渠道 |
| **响应缓存** | 使用 responseRecorder 避免重复写入 |
| **动态管理** | 通过 API 动态增删模型，无需重启 |

## 未来改进方向

1. **持久化**：支持将排名信息保存到数据库或 Redis，重启后恢复
2. **衰减**：时间衰减，旧的成功/失败记录权重降低
3. **监控**：添加 Prometheus 指标
4. **冷却**：频繁失败的模型暂时禁用一段时间
5. **更丰富的统计**：每个模型的详细成功率、延迟统计
