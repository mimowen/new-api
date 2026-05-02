# 模型映射配置说明

## 当前实现方案（已完成）

本次重构实现了**独立架构**的模型拦截器，解决了之前的中间件执行顺序问题。

### 核心设计思路

不再依赖全局中间件的执行顺序，而是：
1. **拦截器作为 handler wrapper** - 在路由级别用 `ModelInterceptorHandler` 包装原始 handler
2. **Distributor 中间件优先读取 context** - 添加 `mapped_model` 检查
3. **拦截器内部实现完整重试** - 使用 responseRecorder 缓存响应，失败时自动切换

### 架构优势

| 方面 | 说明 |
|------|------|
| **零修改 relay.go** | 完全不污染核心逻辑 |
| **独立可维护** | 拦截器逻辑集中在 `model_interceptor.go` |
| **自动重试** | 内部实现重试循环，无需外部介入 |
| **渠道重选** | 重试前清除渠道上下文，确保新模型匹配新渠道 |

---

## 配置说明

### 1. `model_mapping.yaml`

```yaml
enabled: true
score_config:
  initial_score: 40
  success_bonus: 10
  failure_penalty: 20
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
intercept_endpoints:
  - "/v1/chat/completions"
  - "/v1/completions"
  - "/v1/embeddings"
```

### 2. 路由设置

在 `router/relay-router.go` 中用 `ModelInterceptorHandler` 包装需要拦截的路由：

```go
httpRouter.POST("/chat/completions", 
    relay.ModelInterceptorHandler(
        func(c *gin.Context) {
            controller.Relay(c, types.RelayFormatOpenAI)
        }
    )
)
```

### 3. Distributor 检查

在 `middleware/distributor.go` 的 `getModelFromRequest` 中添加：

```go
if mappedModel, exists := c.Get("mapped_model"); exists {
    if modelStr, ok := mappedModel.(string); ok {
        modelRequest.Model = modelStr
        return &modelRequest, nil
    }
}
```

---

## 工作流程

```
1. 请求到达
2. Distributor 中间件运行（但找不到渠道信息，因为 context 是空的）
3. ModelInterceptorHandler 运行
   a. 读取请求体
   b. 确定模型类别
   c. 从 ModelRanker 选择模型
   d. 替换请求体中的模型名
   e. 设置 mapped_model context
4. 原 Relay handler 运行
   a. getChannel 发现 channel meta 为空
   b. 从 distributor 设置的 context（或者重新查询）找到渠道
5. 请求处理完成
6. responseRecorder 缓存响应
7. 检查状态码：
   - < 400: RecordSuccess，刷新响应给客户端
   - ≥ 400: RecordFailure，清除渠道上下文，重试
   - 直到成功或所有模型都尝试过
```

---

## 验证指南

### 1. 检查服务启动
```
[ModelInterceptor] 配置加载成功
[ModelInterceptor] Initialized category default with 5 models
```

### 2. 发送测试请求
```bash
curl -X POST http://localhost:3000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "default",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### 3. 观察日志
```
[ModelInterceptor] 模型替换: default → meta/llama-4-maverick-17B-128E-instruct
[ModelInterceptor] 模型 meta/llama-4-maverick-17B-128E-instruct 请求失败 (status: 404)，尝试下一个
[ModelInterceptor] 切换到下一个模型: MiniMax/MiniMax-M1-80k
[ModelInterceptor] 模型 MiniMax/MiniMax-M1-80k 请求成功
```

---

## 历史方案参考（已废弃）

### 方案一：修改中间件执行顺序（旧方案，已废弃）
修改全局中间件顺序，让拦截器先执行。

**问题**：依赖执行顺序，架构不够清晰。

### 方案二：修改 Distribute 中间件（部分保留）
**保留内容**：添加 `mapped_model` context 检查（仅 8 行）。

### 方案三：使用渠道标签功能
仍然可用，作为补充方案。

---

## 最佳实践

1. **配置正确的模型** - 确保 `model_mapping.yaml` 中的模型在系统 abilities 中已启用
2. **设置合理的 patterns** - 让拦截器能正确匹配
3. **监控排名变化** - 观察日志中的排名调整，优化权重
4. **使用 API 动态管理** - 无需重启，通过 API 增删模型
