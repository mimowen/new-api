# 模型映射配置说明

由于当前 NewAPI 的中间件执行顺序问题，模型拦截器可能无法正常工作。为了解决这个问题，有以下两种方案：

## 方案一：修改中间件执行顺序（推荐）

修改 router/relay-router.go 中的路由设置，调整中间件顺序：

```go
func SetRelayRouter(router *gin.Engine, assets ThemeAssets) {
    // 全局中间件
    router.Use(middleware.CORS())
    router.Use(middleware.DecompressRequestMiddleware())
    router.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
    router.Use(middleware.StatsMiddleware())
    
    // 定义路由组，但暂时不添加 Distribute 中间件
    relayV1Router := router.Group("/v1")
    relayV1Router.Use(middleware.RouteTag("relay"))
    relayV1Router.Use(middleware.TokenAuth())
    relayV1Router.Use(middleware.ModelRequestRateLimit())
    // 注意：这里不添加 Distribute 中间件
    
    // 在具体路由级别添加 ModelInterceptor 和 Distribute
    {
        //http router
        httpRouter := relayV1Router.Group("")
        // 重要：先添加 ModelInterceptor，再添加 Distribute
        httpRouter.Use(relay.ModelInterceptor())  // 模型拦截器
        httpRouter.Use(middleware.Distribute())   // 渠道分发
        
        // chat related routes
        httpRouter.POST("/completions", func(c *gin.Context) {
            controller.Relay(c, types.RelayFormatOpenAI)
        })
        httpRouter.POST("/chat/completions", func(c *gin.Context) {
            controller.Relay(c, types.RelayFormatOpenAI)
        })
        
        // 其他路由...
    }
}
```

## 方案二：修改 Distribute 中间件

修改 middleware/distributor.go 中的 Distribute 函数，使其优先使用上下文中可能存在的模型名称。

## 方案三：使用渠道标签功能

在 NewAPI 控制台中：
1. 创建支持 "qwen3.6-plus-free" 和 "kimi-k2.6-free" 模型的渠道
2. 为这些渠道设置模型别名，将 "gpt-4-code" 等映射到实际模型
3. 在渠道配置中使用模型覆盖功能

## 当前配置验证

你的 model_mapping.yaml 配置是正确的，问题在于中间件执行顺序。

## 测试验证

要验证模型映射是否工作：
1. 在 NewAPI 控制台中添加支持 qwen3.6-plus-free 和 kimi-k2.6-free 的渠道
2. 重启 NewAPI 服务
3. 发送测试请求，查看服务端日志是否有 [ModelInterceptor] 的输出
4. 成功的映射应该显示 "模型替换: gpt-4-code → qwen3.6-plus-free"