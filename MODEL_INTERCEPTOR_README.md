# New API 模型拦截器 - 动态排名系统

## 概述

本次升级为 New API 项目添加了基于动态排名的模型拦截器系统。系统将根据模型的请求成功率，自动调整模型的优先级，提高整体请求成功率。

## 功能特性

### 1. 动态排名管理
- 每个模型类别独立维护排名队列
- 初始排名按配置文件中定义的权重排列
- 使用读写锁确保线程安全（`sync.RWMutex`）

### 2. 排名调整算法
- **成功请求**：模型得分 +10
- **失败请求**：模型得分 -20，最低 0
- 每次记录结果后自动重新排序

### 3. 自动重试和故障转移
- 每次请求优先选择当前排名最高的模型
- 当前模型失败时，自动尝试下一个排名的模型
- 支持记录已尝试过的模型，避免重复

### 4. 完整的日志记录
- `[ModelInterceptor]` - 模型拦截相关日志
- `[ModelRanker]` - 模型排名调整相关日志

## 文件修改

### 1. `relay/model_interceptor.go`
- 完全重写，实现 `ModelRanker` 结构体
- 包含所有核心逻辑

### 2. `controller/relay.go`
- 添加了 `RecordModelResult` 函数
- 修改了 `Relay` 控制器以支持结果记录

### 3. `config/model_mapping.yaml`
- 添加了实际可用的模型配置

## 配置说明

### `model_mapping.yaml` 示例

```yaml
enabled: true
mappings:
  default:
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

## 测试指南

### 1. 本地 Go 测试
```bash
cd d:\project\new-api
go run test_model_rank.go
```

这个脚本将演示：
- 初始排名
- 连续请求后的排名变化
- 失败请求对排名的影响
- 成功请求对排名的提升
- 自动切换机制

### 2. Python API 测试
```bash
cd d:\project\new-api
python test_api.py --status
python test_api.py --simple
python test_api.py
```

### 3. 观察日志
启动服务后，观察日志中的关键信息：
```
[ModelInterceptor] ...
[ModelRanker] ...
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
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### 3. 观察动态排名
- 连续发送几次请求
- 观察日志中的模型选择
- 故意触发失败（如配置无效 API Key），观察排名下降

## 系统架构

```
请求 → ModelInterceptor → 确定类别 → 获取下一个最佳模型
         ↓
    控制器处理
         ↓
    下游服务
         ↓
    RecordResult → 调整模型排名 → 重新排序
```

## 线程安全

- 使用 `sync.RWMutex` 保证并发安全
- 读取操作用 `RLock`
- 写入操作用 `Lock`

## 兼容性

- 完全向后兼容
- 配置项保持不变
- 启用/禁用开关 `enabled` 依然有效

## 未来改进方向

1. **持久化**：支持将排名信息保存到数据库或 Redis，重启后恢复
2. **衰减**：时间衰减，旧的成功/失败记录权重降低
3. **监控**：添加 Prometheus 指标
4. **冷却**：频繁失败的模型暂时禁用一段时间
5. **统计**：提供 API 查看当前排名状态
