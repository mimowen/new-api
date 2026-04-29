package relay

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

var (
	modelConfig     *ModelMappingConfig
	configOnce      sync.Once
	configLoadError error
)

type ModelMappingConfig struct {
	Enabled            bool                      `yaml:"enabled"`
	ScoreConfig        ScoreConfig               `yaml:"score_config"`
	Mappings           map[string]CategoryConfig `yaml:"mappings"`
	InterceptEndpoints []string                  `yaml:"intercept_endpoints"`
}

type ScoreConfig struct {
	InitialScore   float64 `yaml:"initial_score"`
	SuccessBonus   float64 `yaml:"success_bonus"`
	FailurePenalty float64 `yaml:"failure_penalty"`
}

type CategoryConfig struct {
	Patterns []string      `yaml:"patterns"`
	Models   []ModelWeight `yaml:"models"`
}

type ModelWeight struct {
	Model  string `yaml:"model"`
	Weight int    `yaml:"weight"`
}

type RankedModel struct {
	Model     string
	Score     float64
	LastUsed  time.Time
	Successes int
	Failures  int
}

type ModelRanker struct {
	mu       sync.RWMutex
	rankings map[string][]*RankedModel
}

var (
	modelRanker *ModelRanker
	rankerOnce  sync.Once
)

func GetModelRanker() *ModelRanker {
	rankerOnce.Do(func() {
		modelRanker = &ModelRanker{
			rankings: make(map[string][]*RankedModel),
		}
	})
	return modelRanker
}

func (mr *ModelRanker) InitializeCategory(category string, models []ModelWeight, initialScore float64) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if _, exists := mr.rankings[category]; exists {
		return
	}

	if initialScore == 0 {
		initialScore = 40
	}

	rankedModels := make([]*RankedModel, 0, len(models))
	for _, mw := range models {
		rankedModels = append(rankedModels, &RankedModel{
			Model:     mw.Model,
			Score:     initialScore,
			LastUsed:  time.Time{},
			Successes: 0,
			Failures:  0,
		})
	}

	mr.rankings[category] = rankedModels
	logrus.Infof("[ModelRanker] Initialized category %s with %d models (initial score: %.0f)", category, len(rankedModels), initialScore)
}

func (mr *ModelRanker) GetNextModel(category string, excludeModels []string) string {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	rankedModels, exists := mr.rankings[category]
	if !exists || len(rankedModels) == 0 {
		return ""
	}

	excludeSet := make(map[string]bool)
	for _, m := range excludeModels {
		excludeSet[m] = true
	}

	for _, rm := range rankedModels {
		if !excludeSet[rm.Model] {
			return rm.Model
		}
	}

	return ""
}

func getScoreConfig() ScoreConfig {
	if modelConfig != nil {
		return modelConfig.ScoreConfig
	}
	return ScoreConfig{}
}

func (mr *ModelRanker) RecordSuccess(category, model string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	rankedModels, exists := mr.rankings[category]
	if !exists {
		return
	}

	sc := getScoreConfig()
	successBonus := sc.SuccessBonus
	if successBonus == 0 {
		successBonus = 10
	}

	for _, rm := range rankedModels {
		if rm.Model == model {
			rm.Successes++
			rm.Score += successBonus
			rm.LastUsed = time.Now()
			break
		}
	}

	mr.sortRankings(category)
	logrus.Infof("[ModelRanker] Recorded success for %s in category %s (bonus: %.0f)", model, category, successBonus)
}

func (mr *ModelRanker) RecordFailure(category, model string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	rankedModels, exists := mr.rankings[category]
	if !exists {
		return
	}

	sc := getScoreConfig()
	failurePenalty := sc.FailurePenalty
	if failurePenalty == 0 {
		failurePenalty = 20
	}

	for _, rm := range rankedModels {
		if rm.Model == model {
			rm.Failures++
			rm.Score -= failurePenalty
			if rm.Score < 0 {
				rm.Score = 0
			}
			rm.LastUsed = time.Now()
			break
		}
	}

	mr.sortRankings(category)
	logrus.Infof("[ModelRanker] Recorded failure for %s in category %s", model, category)
}

func (mr *ModelRanker) sortRankings(category string) {
	rankedModels := mr.rankings[category]
	for i := range rankedModels {
		for j := i + 1; j < len(rankedModels); j++ {
			if rankedModels[j].Score > rankedModels[i].Score {
				rankedModels[i], rankedModels[j] = rankedModels[j], rankedModels[i]
			}
		}
	}
}

type RankStatus struct {
	Category string            `json:"category"`
	Models   []RankedModelInfo `json:"models"`
}

type RankedModelInfo struct {
	Model     string  `json:"model"`
	Score     float64 `json:"score"`
	Successes int     `json:"successes"`
	Failures  int     `json:"failures"`
}

func (mr *ModelRanker) GetRankStatus() map[string]RankStatus {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	status := make(map[string]RankStatus)
	for category, models := range mr.rankings {
		modelsInfo := make([]RankedModelInfo, 0, len(models))
		for _, m := range models {
			modelsInfo = append(modelsInfo, RankedModelInfo{
				Model:     m.Model,
				Score:     m.Score,
				Successes: m.Successes,
				Failures:  m.Failures,
			})
		}
		status[category] = RankStatus{
			Category: category,
			Models:   modelsInfo,
		}
	}
	return status
}

func LoadModelConfig() (*ModelMappingConfig, error) {
	configOnce.Do(func() {
		data, err := os.ReadFile("model_mapping.yaml")
		if err == nil {
			logrus.Infof("[ModelInterceptor] 配置文件加载成功: model_mapping.yaml")

			tempConfig := &ModelMappingConfig{}
			err = yaml.Unmarshal(data, tempConfig)
			if err != nil {
				logrus.Errorf("[ModelInterceptor] 配置文件解析错误: %v", err)
				fallbackConfig := getDefaultModelConfig()
				modelConfig = &fallbackConfig
				return
			}
			modelConfig = tempConfig
		} else {
			logrus.Info("[ModelInterceptor] 未找到配置文件，使用默认配置（不进行模型映射）")
			defaultConfig := getDefaultModelConfig()
			modelConfig = &defaultConfig
		}

		ranker := GetModelRanker()
		initialScore := modelConfig.ScoreConfig.InitialScore
		for category, cfg := range modelConfig.Mappings {
			ranker.InitializeCategory(category, cfg.Models, initialScore)
		}
	})

	return modelConfig, configLoadError
}

func getDefaultModelConfig() ModelMappingConfig {
	return ModelMappingConfig{
		Enabled:            false,
		Mappings:           map[string]CategoryConfig{},
		InterceptEndpoints: []string{},
	}
}

type modelRetryContext struct {
	category      string
	originalModel string
	triedModels   []string
	currentIndex  int
	body          []byte
}

const (
	modelRetryContextKey = "model_retry_context"
)

func ModelInterceptor() gin.HandlerFunc {
	config, _ := LoadModelConfig()
	if !config.Enabled {
		logrus.Info("[ModelInterceptor] 已禁用，跳过拦截")
		return func(c *gin.Context) { c.Next() }
	}

	return func(c *gin.Context) {
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

		storage, err := common.GetBodyStorage(c)
		if err != nil {
			logrus.Errorf("[ModelInterceptor] 读取请求体失败: %v", err)
			c.Next()
			return
		}

		body, err := storage.Bytes()
		if err != nil {
			logrus.Errorf("[ModelInterceptor] 读取存储数据失败: %v", err)
			c.Next()
			return
		}

		var requestBody map[string]interface{}
		if err := common.Unmarshal(body, &requestBody); err != nil {
			logrus.Errorf("[ModelInterceptor] 解析JSON失败: %v", err)
			c.Next()
			return
		}

		originalModel, ok := requestBody["model"].(string)
		if !ok || originalModel == "" {
			c.Next()
			return
		}

		category := DetermineCategory(originalModel, config)

		var triedModels []string
		if existingCtx, exists := c.Get(modelRetryContextKey); exists {
			if retryCtx, ok := existingCtx.(*modelRetryContext); ok {
				triedModels = retryCtx.triedModels
			}
		}

		ranker := GetModelRanker()
		newModel := ranker.GetNextModel(category, triedModels)

		if newModel == "" {
			newModel = originalModel
			logrus.Warnf("[ModelInterceptor] 没有可用的模型，使用原始模型 %s", originalModel)
		}

		triedModels = append(triedModels, newModel)

		retryCtx := &modelRetryContext{
			category:      category,
			originalModel: originalModel,
			triedModels:   triedModels,
			body:          body,
		}
		c.Set(modelRetryContextKey, retryCtx)

		logrus.Infof("[ModelInterceptor] 模型替换: %s -> %s (category: %s, tried: %v)", originalModel, newModel, category, triedModels)

		requestBody["model"] = newModel

		newBody, err := common.Marshal(requestBody)
		if err != nil {
			logrus.Errorf("[ModelInterceptor] 重新序列化失败: %v", err)
			c.Next()
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
		c.Request.ContentLength = int64(len(newBody))
		c.Set("original_model", originalModel)
		c.Set("mapped_model", newModel)
		c.Set("model_category", category)

		logrus.Infof("[ModelInterceptor] 模型映射: %s -> %s (category: %s)", originalModel, newModel, category)

		c.Next()
	}
}

func DetermineCategory(modelName string, config *ModelMappingConfig) string {
	lowerModel := strings.ToLower(modelName)

	for category, cfg := range config.Mappings {
		for _, pattern := range cfg.Patterns {
			if strings.Contains(lowerModel, strings.ToLower(pattern)) {
				return category
			}
		}
	}

	return "default"
}

func RecordModelResult(c *gin.Context, success bool) {
	category := c.GetString("model_category")
	model := c.GetString("mapped_model")

	if category == "" || model == "" {
		return
	}

	ranker := GetModelRanker()
	if success {
		ranker.RecordSuccess(category, model)
	} else {
		ranker.RecordFailure(category, model)
	}
}

func HasMoreModelsToTry(c *gin.Context) bool {
	existingCtx, exists := c.Get(modelRetryContextKey)
	if !exists {
		return false
	}

	retryCtx, ok := existingCtx.(*modelRetryContext)
	if !ok {
		return false
	}

	if modelConfig == nil {
		return false
	}
	cfg, exists := modelConfig.Mappings[retryCtx.category]
	if !exists {
		return false
	}

	return len(retryCtx.triedModels) < len(cfg.Models)
}

func PrepareNextModel(c *gin.Context) bool {
	existingCtx, exists := c.Get(modelRetryContextKey)
	if !exists {
		return false
	}

	retryCtx, ok := existingCtx.(*modelRetryContext)
	if !ok {
		return false
	}

	if modelConfig == nil {
		return false
	}
	cfg, exists := modelConfig.Mappings[retryCtx.category]
	if !exists || len(retryCtx.triedModels) >= len(cfg.Models) {
		return false
	}

	ranker := GetModelRanker()
	nextModel := ranker.GetNextModel(retryCtx.category, retryCtx.triedModels)
	if nextModel == "" {
		return false
	}

	retryCtx.triedModels = append(retryCtx.triedModels, nextModel)
	c.Set(modelRetryContextKey, retryCtx)

	var requestBody map[string]interface{}
	if err := common.Unmarshal(retryCtx.body, &requestBody); err != nil {
		logrus.Errorf("[ModelInterceptor] 解析原始请求体失败: %v", err)
		return false
	}

	requestBody["model"] = nextModel
	newBody, err := common.Marshal(requestBody)
	if err != nil {
		logrus.Errorf("[ModelInterceptor] 重新序列化失败: %v", err)
		return false
	}

	c.Request.Body = io.NopCloser(bytes.NewBuffer(newBody))
	c.Request.ContentLength = int64(len(newBody))
	c.Set("mapped_model", nextModel)

	logrus.Infof("[ModelInterceptor] 切换到下一个模型: %s (已尝试: %v)", nextModel, retryCtx.triedModels)

	return true
}
