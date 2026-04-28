package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

var (
	modelConfig     *ModelMappingConfig
	configOnce      sync.Once
	configLoadError error
)

var (
	// 全局随机数生成器，线程安全
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	rngMutex sync.Mutex
)

type ModelMappingConfig struct {
	Enabled         bool              `yaml:"enabled"`
	Mappings        map[string]CategoryConfig `yaml:"mappings"`
	InterceptEndpoints []string         `yaml:"intercept_endpoints"`
}

type CategoryConfig struct {
	Patterns []string         `yaml:"patterns"`
	Models   []ModelWeight    `yaml:"models"`
}

type ModelWeight struct {
	Model  string `yaml:"model"`
	Weight int    `yaml:"weight"`
}

func LoadModelConfig() (*ModelMappingConfig, error) {
	configOnce.Do(func() {
		// 只尝试加载单一配置文件
		data, err := ioutil.ReadFile("model_mapping.yaml")
		if err == nil {
			// 如果配置文件存在，使用它
			logrus.Infof("[ModelInterceptor] 配置文件加载成功: model_mapping.yaml")
			
			tempConfig := &ModelMappingConfig{}
			err = yaml.Unmarshal(data, tempConfig)
			if err != nil {
				logrus.Errorf("[ModelInterceptor] 配置文件解析错误: %v", err)
				// 解析失败时回退到默认配置
				fallbackConfig := getDefaultModelConfig()
				modelConfig = &fallbackConfig
				return
			}
			modelConfig = tempConfig
			return
		}
		
		// 如果配置文件不存在，使用默认配置（即不进行任何模型映射）
		logrus.Info("[ModelInterceptor] 未找到配置文件，使用默认配置（不进行模型映射）")
		defaultConfig := getDefaultModelConfig()
		modelConfig = &defaultConfig
	})

	return modelConfig, configLoadError
}

func getDefaultModelConfig() ModelMappingConfig {
	return ModelMappingConfig{
		Enabled: true,
		Mappings: map[string]CategoryConfig{
			"code": {
				Patterns: []string{"code", "coder", "programming", "dev"},
				Models: []ModelWeight{
					{Model: "gpt-4", Weight: 40},
					{Model: "claude-3-sonnet", Weight: 30},
					{Model: "deepseek-coder", Weight: 30},
				},
			},
			"agent": {
				Patterns: []string{"agent", "function", "tool", "assistant"},
				Models: []ModelWeight{
					{Model: "gpt-4o", Weight: 50},
					{Model: "claude-3-5-sonnet", Weight: 30},
					{Model: "qwen-max", Weight: 20},
				},
			},
			"vision": {
				Patterns: []string{"vision", "vl", "image", "visual", "gpt-4-vision"},
				Models: []ModelWeight{
					{Model: "gpt-4o", Weight: 60},
					{Model: "claude-3-5-sonnet", Weight: 40},
				},
			},
			"embedding": {
				Patterns: []string{"embedding", "embed", "text-embedding"},
				Models: []ModelWeight{
					{Model: "text-embedding-ada-002", Weight: 100},
				},
			},
			"default": {
				Patterns: []string{},
				Models: []ModelWeight{
					{Model: "gpt-3.5-turbo", Weight: 60},
					{Model: "claude-3-haiku", Weight: 40},
				},
			},
		},
		InterceptEndpoints: []string{
			"/v1/chat/completions",
			"/v1/completions",
			"/v1/embeddings",
		},
	}
}

func ModelInterceptor() gin.HandlerFunc {
	config, _ := LoadModelConfig()
	if !config.Enabled {
		logrus.Info("[ModelInterceptor] 已禁用，跳过拦截")
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
		// 检查是否需要拦截
		shouldIntercept := false
		for _, endpoint := range config.InterceptEndpoints {
			if strings.HasPrefix(c.Request.URL.Path, endpoint) {
				shouldIntercept = true
				break
			}
		}

		if !shouldIntercept {
			c.Next()
			return
		}

		// 读取原始请求体
		body, err := ioutil.ReadAll(c.Request.Body)
		if err != nil {
			logrus.Errorf("[ModelInterceptor] 读取请求体失败: %v", err)
			c.Next()
			return
		}
		defer c.Request.Body.Close()

		// 解析JSON
		var requestBody map[string]interface{}
		if err := json.Unmarshal(body, &requestBody); err != nil {
			logrus.Errorf("[ModelInterceptor] 解析JSON失败: %v", err)
			c.Next()
			return
		}

		// 获取原始模型
		originalModel, ok := requestBody["model"].(string)
		if !ok || originalModel == "" {
			c.Next()
			return
		}

		// 确定类别
		category := DetermineCategory(originalModel, config)
		
		// 选择新模型
		newModel := SelectModelByCategory(category, config)
		
		// 记录日志
		logrus.Infof("[ModelInterceptor] 模型替换: %s → %s (category: %s)", originalModel, newModel, category)

		// 替换模型
		requestBody["model"] = newModel

		// 重新序列化请求体
		newBody, err := json.Marshal(requestBody)
		if err != nil {
			logrus.Errorf("[ModelInterceptor] 重新序列化失败: %v", err)
			c.Next()
			return
		}

		// 创建新的请求体
		c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
		c.Request.ContentLength = int64(len(newBody))

		// 在上下文中设置原始模型名称，以便后续中间件可以使用
		c.Set("original_model", originalModel)
		c.Set("mapped_model", newModel)

		// 记录模型映射日志
		logrus.Infof("[ModelInterceptor] 模型映射: %s → %s (category: %s)", originalModel, newModel, category)

		c.Next()
	}
}

// DetermineCategory determines the category of a model based on its name and patterns
func DetermineCategory(modelName string, config *ModelMappingConfig) string {
	lowerModel := strings.ToLower(modelName)
	
	// 检查每个类别的模式
	for category, cfg := range config.Mappings {
		for _, pattern := range cfg.Patterns {
			if strings.Contains(lowerModel, strings.ToLower(pattern)) {
				return category
			}
		}
	}
	
	return "default"
}

// SelectModelByCategory selects a model based on the determined category
func SelectModelByCategory(category string, config *ModelMappingConfig) string {
	cfg, exists := config.Mappings[category]
	if !exists {
		cfg, exists = config.Mappings["default"]
		if !exists {
			return "gpt-3.5-turbo"
		}
	}
	
	if len(cfg.Models) == 0 {
		return "gpt-3.5-turbo"
	}
	
	return SelectModelByWeight(cfg.Models)
}

// SelectModelByWeight selects a model based on weight distribution
func SelectModelByWeight(models []ModelWeight) string {
	totalWeight := 0
	for _, m := range models {
		totalWeight += m.Weight
	}
	
	if totalWeight <= 0 {
		return models[0].Model
	}
	
	rngMutex.Lock()
	random := rng.Intn(totalWeight)
	rngMutex.Unlock()
	
	current := 0
	for _, model := range models {
		current += model.Weight
		if random < current {
			return model.Model
		}
	}
	
	return models[len(models)-1].Model
}