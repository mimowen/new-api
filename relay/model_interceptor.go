package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
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
		initialScore = 100
	}

	enabledModels := model.GetEnabledModels()
	enabledSet := make(map[string]bool)
	for _, m := range enabledModels {
		enabledSet[strings.ToLower(m)] = true
	}

	rankedModels := make([]*RankedModel, 0, len(models))
	for _, mw := range models {
		if !enabledSet[strings.ToLower(mw.Model)] {
			common.SysLog(fmt.Sprintf("[ModelRanker] 模型 %s 不在系统可用模型中，已从 %s 分组移除", mw.Model, category))
			continue
		}
		rankedModels = append(rankedModels, &RankedModel{
			Model:     mw.Model,
			Score:     initialScore,
			LastUsed:  time.Time{},
			Successes: 0,
			Failures:  0,
		})
	}

	mr.rankings[category] = rankedModels
	common.SysLog(fmt.Sprintf("[ModelRanker] Initialized category %s with %d models (initial score: %.0f)", category, len(rankedModels), initialScore))
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
	common.SysLog(fmt.Sprintf("[ModelRanker] Recorded success for %s in category %s (bonus: %.0f)", model, category, successBonus))
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
		failurePenalty = 5
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
	common.SysLog(fmt.Sprintf("[ModelRanker] Recorded failure for %s in category %s (penalty: %.0f)", model, category, failurePenalty))
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

func (mr *ModelRanker) AddModel(category, modelName string, initialScore float64) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if initialScore == 0 {
		initialScore = 100
	}

	rankedModels, exists := mr.rankings[category]
	if !exists {
		mr.rankings[category] = []*RankedModel{
			{
				Model:     modelName,
				Score:     initialScore,
				LastUsed:  time.Time{},
				Successes: 0,
				Failures:  0,
			},
		}
		common.SysLog(fmt.Sprintf("[ModelRanker] Added model %s to category %s (score: %.0f)", modelName, category, initialScore))
		return
	}

	for _, rm := range rankedModels {
		if rm.Model == modelName {
			common.SysLog(fmt.Sprintf("[ModelRanker] Model %s already exists in category %s", modelName, category))
			return
		}
	}

	mr.rankings[category] = append(rankedModels, &RankedModel{
		Model:     modelName,
		Score:     initialScore,
		LastUsed:  time.Time{},
		Successes: 0,
		Failures:  0,
	})
	mr.sortRankings(category)
	common.SysLog(fmt.Sprintf("[ModelRanker] Added model %s to category %s (score: %.0f)", modelName, category, initialScore))
}

func (mr *ModelRanker) RemoveModel(category, modelName string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	rankedModels, exists := mr.rankings[category]
	if !exists {
		return
	}

	for i, rm := range rankedModels {
		if rm.Model == modelName {
			mr.rankings[category] = append(rankedModels[:i], rankedModels[i+1:]...)
			common.SysLog(fmt.Sprintf("[ModelRanker] Removed model %s from category %s", modelName, category))
			return
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

func (mr *ModelRanker) GetCategoryModelCount(category string) int {
	mr.mu.RLock()
	defer mr.mu.RUnlock()

	rankedModels, exists := mr.rankings[category]
	if !exists {
		return 0
	}
	return len(rankedModels)
}

func LoadModelConfig() (*ModelMappingConfig, error) {
	configOnce.Do(func() {
		configPaths := []string{"model_mapping.yaml", "/data/model_mapping.yaml"}
		var foundData []byte
		var foundPath string

		for _, path := range configPaths {
			data, err := os.ReadFile(path)
			if err == nil {
				foundData = data
				foundPath = path
				break
			}
		}

		if foundData != nil {
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 配置文件加载成功: %s", foundPath))

			tempConfig := &ModelMappingConfig{}
			err := yaml.Unmarshal(foundData, tempConfig)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 配置文件解析错误: %v", err))
				fallbackConfig := getDefaultModelConfig()
				modelConfig = &fallbackConfig
				return
			}
			modelConfig = tempConfig
		} else {
			common.SysLog("[ModelInterceptor] 未找到配置文件，使用默认配置（不进行模型映射）")
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
	body          []byte
	intercepted   bool
}

const (
	modelRetryContextKey = "model_retry_context"
)

func ModelInterceptor() gin.HandlerFunc {
	config, _ := LoadModelConfig()
	if !config.Enabled {
		common.SysLog("[ModelInterceptor] 已禁用，跳过拦截")
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
			common.SysError(fmt.Sprintf("[ModelInterceptor] 读取请求体失败: %v", err))
			c.Next()
			return
		}

		body, err := storage.Bytes()
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 读取存储数据失败: %v", err))
			c.Next()
			return
		}

		var requestBody map[string]json.RawMessage
		if err := common.Unmarshal(body, &requestBody); err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 解析JSON失败: %v", err))
			c.Next()
			return
		}

		var originalModel string
		if modelRaw, ok := requestBody["model"]; ok {
			if err := common.Unmarshal(modelRaw, &originalModel); err != nil {
				c.Next()
				return
			}
		}
		if originalModel == "" {
			c.Next()
			return
		}

		category := DetermineCategory(originalModel, config)

		if category == "" {
			c.Set(modelRetryContextKey, &modelRetryContext{
				originalModel: originalModel,
				intercepted:   false,
			})
			c.Next()
			return
		}

		var triedModels []string
		if existingCtx, exists := c.Get(modelRetryContextKey); exists {
			if retryCtx, ok := existingCtx.(*modelRetryContext); ok {
				triedModels = retryCtx.triedModels
			}
		}

		ranker := GetModelRanker()
		newModel := ranker.GetNextModel(category, triedModels)

		if newModel == "" {
			c.Set(modelRetryContextKey, &modelRetryContext{
				category:      category,
				originalModel: originalModel,
				intercepted:   false,
			})
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 无可用模型，透传原始模型 %s", category, originalModel))
			c.Next()
			return
		}

		triedModels = append(triedModels, newModel)

		retryCtx := &modelRetryContext{
			category:      category,
			originalModel: originalModel,
			triedModels:   triedModels,
			body:          body,
			intercepted:   true,
		}
		c.Set(modelRetryContextKey, retryCtx)

		common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型替换: %s -> %s (category: %s)", originalModel, newModel, category))

		requestBody["model"], _ = common.Marshal(newModel)

		newBody, err := common.Marshal(requestBody)
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 重新序列化失败: %v", err))
			c.Next()
			return
		}

		// 创建新的 BodyStorage 并更新缓存
		newStorage, err := common.CreateBodyStorage(newBody)
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 创建新存储失败: %v", err))
			c.Next()
			return
		}

		// 更新 c.Request.Body 和缓存
		c.Request.Body = io.NopCloser(newStorage)
		c.Request.ContentLength = int64(len(newBody))
		c.Set(common.KeyBodyStorage, newStorage) // 关键：更新缓存！

		c.Set("original_model", originalModel)
		c.Set("mapped_model", newModel)
		c.Set("model_category", category)

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

	return ""
}

func RecordModelResult(c *gin.Context, success bool) {
	existingCtx, exists := c.Get(modelRetryContextKey)
	if !exists {
		return
	}

	retryCtx, ok := existingCtx.(*modelRetryContext)
	if !ok {
		return
	}

	if !retryCtx.intercepted {
		return
	}

	category := retryCtx.category
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

	if !retryCtx.intercepted {
		return false
	}

	ranker := GetModelRanker()
	count := ranker.GetCategoryModelCount(retryCtx.category)

	return len(retryCtx.triedModels) < count
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

	if !retryCtx.intercepted {
		return false
	}

	ranker := GetModelRanker()
	nextModel := ranker.GetNextModel(retryCtx.category, retryCtx.triedModels)
	if nextModel == "" {
		return false
	}

	retryCtx.triedModels = append(retryCtx.triedModels, nextModel)
	c.Set(modelRetryContextKey, retryCtx)

	var requestBody map[string]json.RawMessage
	if err := common.Unmarshal(retryCtx.body, &requestBody); err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] 解析原始请求体失败: %v", err))
		return false
	}

	requestBody["model"], _ = common.Marshal(nextModel)
	newBody, err := common.Marshal(requestBody)
	if err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] 重新序列化失败: %v", err))
		return false
	}

	// 创建新的 BodyStorage 并更新缓存
	newStorage, err := common.CreateBodyStorage(newBody)
	if err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] 创建新存储失败: %v", err))
		return false
	}

	// 更新 c.Request.Body 和缓存
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(newBody))
	c.Set(common.KeyBodyStorage, newStorage) // 关键：更新缓存！

	c.Set("mapped_model", nextModel)

	common.SysLog(fmt.Sprintf("[ModelInterceptor] 切换到下一个模型: %s (已尝试: %v)", nextModel, retryCtx.triedModels))

	return true
}
