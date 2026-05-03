package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
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
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型 %s 不在系统可用模型中，已从 %s 分组移除", mw.Model, category))
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
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Initialized category %s with %d models (initial score: %.0f)", category, len(rankedModels), initialScore))
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

func (mr *ModelRanker) RecordSuccess(category, modelName string) {
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
		if rm.Model == modelName {
			rm.Successes++
			rm.Score += successBonus
			rm.LastUsed = time.Now()
			break
		}
	}

	mr.sortRankings(category)
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Recorded success for %s in category %s (bonus: %.0f)", modelName, category, successBonus))
}

func (mr *ModelRanker) RecordFailure(category, modelName string) {
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
		if rm.Model == modelName {
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
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Recorded failure for %s in category %s (penalty: %.0f)", modelName, category, failurePenalty))
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
		common.SysLog(fmt.Sprintf("[ModelInterceptor] Added model %s to category %s (score: %.0f)", modelName, category, initialScore))
		return
	}

	for _, rm := range rankedModels {
		if rm.Model == modelName {
			common.SysLog(fmt.Sprintf("[ModelInterceptor] Model %s already exists in category %s", modelName, category))
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
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Added model %s to category %s (score: %.0f)", modelName, category, initialScore))
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
			common.SysLog(fmt.Sprintf("[ModelInterceptor] Removed model %s from category %s", modelName, category))
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

type responseRecorder struct {
	original   gin.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
	size       int
	written    bool
}

func newResponseRecorder(original gin.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		original:   original,
		statusCode: 200,
		header:     make(http.Header),
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.written {
		r.written = true
	}
	r.size += len(data)
	return r.body.Write(data)
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (r *responseRecorder) Status() int {
	return r.statusCode
}

func (r *responseRecorder) Size() int {
	return r.size
}

func (r *responseRecorder) Written() bool {
	return r.written
}

func (r *responseRecorder) WriteString(s string) (int, error) {
	return r.Write([]byte(s))
}

func (r *responseRecorder) WriteHeaderNow() {}

func (r *responseRecorder) Flush() {}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return r.original.Hijack()
}

func (r *responseRecorder) CloseNotify() <-chan bool {
	return r.original.CloseNotify()
}

func (r *responseRecorder) Pusher() http.Pusher {
	if pusher, ok := r.original.(http.Pusher); ok {
		return pusher
	}
	return nil
}

func (r *responseRecorder) FlushToOriginal() {
	for k, v := range r.header {
		r.original.Header()[k] = v
	}
	r.original.WriteHeader(r.statusCode)
	r.original.Write(r.body.Bytes())
}

func shouldInterceptRequest(path string, config *ModelMappingConfig) bool {
	for _, endpoint := range config.InterceptEndpoints {
		if strings.HasPrefix(path, endpoint) {
			return true
		}
	}
	return false
}

func clearChannelContext(c *gin.Context) {
	channelKeys := []string{
		string(constant.ContextKeyChannelId),
		string(constant.ContextKeyChannelName),
		string(constant.ContextKeyChannelCreateTime),
		string(constant.ContextKeyChannelBaseUrl),
		string(constant.ContextKeyChannelType),
		string(constant.ContextKeyChannelSetting),
		string(constant.ContextKeyChannelOtherSetting),
		string(constant.ContextKeyChannelParamOverride),
		string(constant.ContextKeyChannelHeaderOverride),
		string(constant.ContextKeyChannelOrganization),
		string(constant.ContextKeyChannelAutoBan),
		string(constant.ContextKeyChannelModelMapping),
		string(constant.ContextKeyChannelStatusCodeMapping),
		string(constant.ContextKeyChannelIsMultiKey),
		string(constant.ContextKeyChannelMultiKeyIndex),
		string(constant.ContextKeyChannelKey),
		string(constant.ContextKeyOriginalModel),
		"api_version",
		"region",
	}
	for _, key := range channelKeys {
		c.Set(key, nil)
	}
}

func ModelInterceptorMiddleware() gin.HandlerFunc {
	config, _ := LoadModelConfig()
	if !config.Enabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		if !shouldInterceptRequest(c.Request.URL.Path, config) {
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
			c.Next()
			return
		}

		ranker := GetModelRanker()
		triedModels := []string{}
		originalWriter := c.Writer

		for {
			newModel := ranker.GetNextModel(category, triedModels)
			if newModel == "" {
				if len(triedModels) == 0 {
					common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 无可用模型，透传原始请求", category))
					c.Writer = originalWriter
					c.Next()
					return
				}
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 所有模型已尝试完毕，返回最后错误", category))
				c.Writer = originalWriter
				return
			}

			triedModels = append(triedModels, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型替换: %s -> %s (category: %s, 尝试: %d/%d)", originalModel, newModel, category, len(triedModels), ranker.GetCategoryModelCount(category)))

			parsedBody := make(map[string]json.RawMessage)
			if err := common.Unmarshal(body, &parsedBody); err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 解析原始请求体失败: %v", err))
				c.Writer = originalWriter
				c.Next()
				return
			}

			parsedBody["model"], _ = common.Marshal(newModel)
			newBody, err := common.Marshal(parsedBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 重新序列化失败: %v", err))
				c.Writer = originalWriter
				c.Next()
				return
			}

			newStorage, err := common.CreateBodyStorage(newBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 创建新存储失败: %v", err))
				c.Writer = originalWriter
				c.Next()
				return
			}

			c.Request.Body = io.NopCloser(newStorage)
			c.Request.ContentLength = int64(len(newBody))
			c.Set(common.KeyBodyStorage, newStorage)
			c.Set("original_model", originalModel)
			c.Set("mapped_model", newModel)
			c.Set("model_category", category)

			if len(triedModels) > 1 {
				clearChannelContext(c)
			}

			rec := newResponseRecorder(originalWriter)
			c.Writer = rec

			c.Next()

			if rec.Status() < 400 {
				rec.FlushToOriginal()
				ranker.RecordSuccess(category, newModel)
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型 %s 请求成功", newModel))
				return
			}

			ranker.RecordFailure(category, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型 %s 请求失败 (status: %d)，尝试下一个模型", newModel, rec.Status()))

			c.Writer = originalWriter

			if len(triedModels) >= ranker.GetCategoryModelCount(category) {
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 所有模型已尝试完毕，返回最后错误", category))
				lastRec := newResponseRecorder(originalWriter)
				lastRec.header = rec.header
				lastRec.statusCode = rec.statusCode
				lastRec.body = rec.body
				lastRec.FlushToOriginal()
				return
			}
		}
	}
}

// ModelInterceptorHandler is deprecated, use ModelInterceptorMiddleware instead
func ModelInterceptorHandler(handler gin.HandlerFunc) gin.HandlerFunc {
	config, _ := LoadModelConfig()
	if !config.Enabled {
		return handler
	}

	return func(c *gin.Context) {
		if !shouldInterceptRequest(c.Request.URL.Path, config) {
			handler(c)
			return
		}

		storage, err := common.GetBodyStorage(c)
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 读取请求体失败: %v", err))
			handler(c)
			return
		}

		body, err := storage.Bytes()
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 读取存储数据失败: %v", err))
			handler(c)
			return
		}

		var requestBody map[string]json.RawMessage
		if err := common.Unmarshal(body, &requestBody); err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] 解析JSON失败: %v", err))
			handler(c)
			return
		}

		var originalModel string
		if modelRaw, ok := requestBody["model"]; ok {
			if err := common.Unmarshal(modelRaw, &originalModel); err != nil {
				handler(c)
				return
			}
		}
		if originalModel == "" {
			handler(c)
			return
		}

		category := DetermineCategory(originalModel, config)
		if category == "" {
			handler(c)
			return
		}

		ranker := GetModelRanker()
		triedModels := []string{}
		originalWriter := c.Writer

		for {
			newModel := ranker.GetNextModel(category, triedModels)
			if newModel == "" {
				if len(triedModels) == 0 {
					common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 无可用模型，透传原始请求", category))
					c.Writer = originalWriter
					handler(c)
					return
				}
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 所有模型已尝试完毕，返回最后错误", category))
				c.Writer = originalWriter
				return
			}

			triedModels = append(triedModels, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型替换: %s -> %s (category: %s, 尝试: %d/%d)", originalModel, newModel, category, len(triedModels), ranker.GetCategoryModelCount(category)))

			parsedBody := make(map[string]json.RawMessage)
			if err := common.Unmarshal(body, &parsedBody); err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 解析原始请求体失败: %v", err))
				c.Writer = originalWriter
				handler(c)
				return
			}

			parsedBody["model"], _ = common.Marshal(newModel)
			newBody, err := common.Marshal(parsedBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 重新序列化失败: %v", err))
				c.Writer = originalWriter
				handler(c)
				return
			}

			newStorage, err := common.CreateBodyStorage(newBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] 创建新存储失败: %v", err))
				c.Writer = originalWriter
				handler(c)
				return
			}

			c.Request.Body = io.NopCloser(newStorage)
			c.Request.ContentLength = int64(len(newBody))
			c.Set(common.KeyBodyStorage, newStorage)
			c.Set("original_model", originalModel)
			c.Set("mapped_model", newModel)
			c.Set("model_category", category)

			if len(triedModels) > 1 {
				clearChannelContext(c)
			}

			rec := newResponseRecorder(originalWriter)
			c.Writer = rec

			handler(c)

			if rec.Status() < 400 {
				rec.FlushToOriginal()
				ranker.RecordSuccess(category, newModel)
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型 %s 请求成功", newModel))
				return
			}

			ranker.RecordFailure(category, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] 模型 %s 请求失败 (status: %d)，尝试下一个模型", newModel, rec.Status()))

			c.Writer = originalWriter

			if len(triedModels) >= ranker.GetCategoryModelCount(category) {
				common.SysLog(fmt.Sprintf("[ModelInterceptor] 分类 %s 所有模型已尝试完毕，返回最后错误", category))
				lastRec := newResponseRecorder(originalWriter)
				lastRec.header = rec.header
				lastRec.statusCode = rec.statusCode
				lastRec.body = rec.body
				lastRec.FlushToOriginal()
				return
			}
		}
	}
}
