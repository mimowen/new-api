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
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type ModelMappingConfig struct {
	Enabled            bool                      `yaml:"enabled" json:"enabled"`
	ScoreConfig        ScoreConfig               `yaml:"score_config" json:"score_config"`
	Mappings           map[string]CategoryConfig `yaml:"mappings" json:"mappings"`
	InterceptEndpoints []string                  `yaml:"intercept_endpoints" json:"intercept_endpoints"`
}

type ScoreConfig struct {
	InitialScore   float64 `yaml:"initial_score" json:"initial_score"`
	SuccessBonus   float64 `yaml:"success_bonus" json:"success_bonus"`
	FailurePenalty float64 `yaml:"failure_penalty" json:"failure_penalty"`
}

type CategoryConfig struct {
	Patterns []string      `yaml:"patterns" json:"patterns"`
	Models   []ModelWeight `yaml:"models" json:"models"`
}

type ModelWeight struct {
	Model  string `yaml:"model" json:"model"`
	Weight int    `yaml:"weight" json:"weight"`
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
			common.SysLog(fmt.Sprintf("[ModelInterceptor] model %s not in enabled models, removed from %s", mw.Model, category))
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

func (mr *ModelRanker) RecordSuccess(category, modelName string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	rankedModels, exists := mr.rankings[category]
	if !exists {
		return
	}

	cfg := GetConfig()
	successBonus := cfg.ScoreConfig.SuccessBonus
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

	cfg := GetConfig()
	failurePenalty := cfg.ScoreConfig.FailurePenalty
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

func (mr *ModelRanker) AddCategory(category string, patterns []string, models []ModelWeight, initialScore float64) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	if _, exists := mr.rankings[category]; exists {
		return
	}

	if initialScore == 0 {
		initialScore = 100
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
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Added category %s with %d models", category, len(rankedModels)))
}

func (mr *ModelRanker) RemoveCategory(category string) {
	mr.mu.Lock()
	defer mr.mu.Unlock()

	delete(mr.rankings, category)
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Removed category %s", category))
}

type RankStatus struct {
	Category string            `json:"category"`
	Models   []RankedModelInfo `json:"models"`
	Patterns []string          `json:"patterns"`
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

	cfg := GetConfig()
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
		var patterns []string
		if catCfg, ok := cfg.Mappings[category]; ok {
			patterns = catCfg.Patterns
		}
		status[category] = RankStatus{
			Category: category,
			Models:   modelsInfo,
			Patterns: patterns,
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

var (
	configPtr      atomic.Pointer[ModelMappingConfig]
	configFileMu   sync.Mutex
	configLoaded   atomic.Bool
	captureEnabled atomic.Bool
)

func GetConfig() *ModelMappingConfig {
	if p := configPtr.Load(); p != nil {
		return p
	}
	return &ModelMappingConfig{}
}

func IsInterceptorEnabled() bool {
	return GetConfig().Enabled
}

func SetInterceptorEnabled(enabled bool) {
	cfg := GetConfig()
	newCfg := *cfg
	newCfg.Enabled = enabled
	configPtr.Store(&newCfg)
	mode := "passthrough"
	if enabled {
		mode = "intercept"
	}
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Mode switched to: %s", mode))
}

func IsCaptureEnabled() bool {
	return captureEnabled.Load()
}

func SetCaptureEnabled(enabled bool) {
	captureEnabled.Store(enabled)
	mode := "off"
	if enabled {
		mode = "on"
	}
	common.SysLog(fmt.Sprintf("[ModelInterceptor] Capture mode: %s", mode))
}

func LoadModelConfig() (*ModelMappingConfig, error) {
	if configLoaded.Load() {
		return GetConfig(), nil
	}

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
		common.SysLog(fmt.Sprintf("[ModelInterceptor] Config loaded: %s", foundPath))

		tempConfig := &ModelMappingConfig{}
		err := yaml.Unmarshal(foundData, tempConfig)
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] Config parse error: %v", err))
			fallbackConfig := getDefaultModelConfig()
			configPtr.Store(&fallbackConfig)
			configLoaded.Store(true)
			return GetConfig(), err
		}
		configPtr.Store(tempConfig)
	} else {
		common.SysLog("[ModelInterceptor] No config file found, using default (disabled)")
		defaultConfig := getDefaultModelConfig()
		configPtr.Store(&defaultConfig)
	}

	cfg := GetConfig()
	ranker := GetModelRanker()
	initialScore := cfg.ScoreConfig.InitialScore
	for category, catCfg := range cfg.Mappings {
		ranker.InitializeCategory(category, catCfg.Models, initialScore)
	}

	configLoaded.Store(true)
	return GetConfig(), nil
}

func ReloadModelConfig() (*ModelMappingConfig, error) {
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

	if foundData == nil {
		return GetConfig(), fmt.Errorf("no config file found")
	}

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Reloading config: %s", foundPath))

	tempConfig := &ModelMappingConfig{}
	err := yaml.Unmarshal(foundData, tempConfig)
	if err != nil {
		return GetConfig(), fmt.Errorf("config parse error: %v", err)
	}

	wasEnabled := GetConfig().Enabled
	configPtr.Store(tempConfig)

	ranker := GetModelRanker()
	initialScore := tempConfig.ScoreConfig.InitialScore
	for category, catCfg := range tempConfig.Mappings {
		ranker.InitializeCategory(category, catCfg.Models, initialScore)
	}

	if wasEnabled != tempConfig.Enabled {
		mode := "passthrough"
		if tempConfig.Enabled {
			mode = "intercept"
		}
		common.SysLog(fmt.Sprintf("[ModelInterceptor] Mode changed to: %s after reload", mode))
	}

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Config reloaded successfully"))
	return GetConfig(), nil
}

func SaveModelConfig() error {
	configFileMu.Lock()
	defer configFileMu.Unlock()

	cfg := GetConfig()
	ranker := GetModelRanker()

	saveCfg := *cfg
	saveCfg.Mappings = make(map[string]CategoryConfig)

	status := ranker.GetRankStatus()
	for category, catStatus := range status {
		models := make([]ModelWeight, 0, len(catStatus.Models))
		for _, m := range catStatus.Models {
			models = append(models, ModelWeight{
				Model:  m.Model,
				Weight: 1,
			})
		}
		patterns := catStatus.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		saveCfg.Mappings[category] = CategoryConfig{
			Patterns: patterns,
			Models:   models,
		}
	}

	data, err := yaml.Marshal(&saveCfg)
	if err != nil {
		return fmt.Errorf("yaml marshal error: %v", err)
	}

	configPaths := []string{"model_mapping.yaml", "/data/model_mapping.yaml"}
	writePath := configPaths[0]
	for _, path := range configPaths {
		if _, err := os.Stat(path); err == nil {
			writePath = path
			break
		}
	}

	err = os.WriteFile(writePath, data, 0644)
	if err != nil {
		return fmt.Errorf("write file error: %v", err)
	}

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Config saved to %s", writePath))
	return nil
}

func AddCategoryToConfig(category string, patterns []string) error {
	cfg := GetConfig()
	newCfg := *cfg
	if newCfg.Mappings == nil {
		newCfg.Mappings = make(map[string]CategoryConfig)
	}
	if _, exists := newCfg.Mappings[category]; exists {
		return fmt.Errorf("category %s already exists", category)
	}
	newCfg.Mappings[category] = CategoryConfig{
		Patterns: patterns,
		Models:   []ModelWeight{},
	}
	configPtr.Store(&newCfg)

	ranker := GetModelRanker()
	initialScore := newCfg.ScoreConfig.InitialScore
	ranker.AddCategory(category, patterns, []ModelWeight{}, initialScore)

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Added category %s with patterns %v", category, patterns))
	return nil
}

func RemoveCategoryFromConfig(category string) error {
	cfg := GetConfig()
	newCfg := *cfg
	if newCfg.Mappings == nil {
		return fmt.Errorf("category %s not found", category)
	}
	if _, exists := newCfg.Mappings[category]; !exists {
		return fmt.Errorf("category %s not found", category)
	}
	delete(newCfg.Mappings, category)
	configPtr.Store(&newCfg)

	ranker := GetModelRanker()
	ranker.RemoveCategory(category)

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Removed category %s from config", category))
	return nil
}

func AddModelToConfig(category, modelName string, weight int) error {
	cfg := GetConfig()
	newCfg := *cfg
	if newCfg.Mappings == nil {
		return fmt.Errorf("category %s not found", category)
	}
	catCfg, exists := newCfg.Mappings[category]
	if !exists {
		return fmt.Errorf("category %s not found", category)
	}

	for _, m := range catCfg.Models {
		if m.Model == modelName {
			return fmt.Errorf("model %s already exists in category %s", modelName, category)
		}
	}

	catCfg.Models = append(catCfg.Models, ModelWeight{Model: modelName, Weight: weight})
	newCfg.Mappings[category] = catCfg
	configPtr.Store(&newCfg)

	initialScore := newCfg.ScoreConfig.InitialScore
	ranker := GetModelRanker()
	ranker.AddModel(category, modelName, initialScore)

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Added model %s to category %s in config", modelName, category))
	return nil
}

func RemoveModelFromConfig(category, modelName string) error {
	cfg := GetConfig()
	newCfg := *cfg
	if newCfg.Mappings == nil {
		return fmt.Errorf("category %s not found", category)
	}
	catCfg, exists := newCfg.Mappings[category]
	if !exists {
		return fmt.Errorf("category %s not found", category)
	}

	found := false
	newModels := make([]ModelWeight, 0, len(catCfg.Models))
	for _, m := range catCfg.Models {
		if m.Model == modelName {
			found = true
			continue
		}
		newModels = append(newModels, m)
	}
	if !found {
		return fmt.Errorf("model %s not found in category %s", modelName, category)
	}

	catCfg.Models = newModels
	newCfg.Mappings[category] = catCfg
	configPtr.Store(&newCfg)

	ranker := GetModelRanker()
	ranker.RemoveModel(category, modelName)

	common.SysLog(fmt.Sprintf("[ModelInterceptor] Removed model %s from category %s in config", modelName, category))
	return nil
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
	flushed    bool
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

func (r *responseRecorder) Flush() {
	r.flushed = true
}

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
	if r.original == nil {
		common.SysError("[ModelInterceptor] FlushToOriginal: original writer is nil")
		return
	}
	for k, v := range r.header {
		r.original.Header()[k] = v
	}
	r.original.WriteHeader(r.statusCode)
	bodyBytes := r.body.Bytes()
	n, err := r.original.Write(bodyBytes)
	common.SysLog(fmt.Sprintf("[ModelInterceptor] FlushToOriginal: statusCode=%d, wrote %d bytes (bodyLen=%d), error: %v", r.statusCode, n, len(bodyBytes), err))
	if r.flushed {
		r.original.Flush()
	}
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
		delete(c.Keys, key)
	}
	delete(c.Keys, "event_stream_headers_set")
}

func ModelInterceptorMiddleware() gin.HandlerFunc {
	LoadModelConfig()

	return func(c *gin.Context) {
		cfg := GetConfig()

		if !cfg.Enabled {
			c.Next()
			return
		}

		if !shouldInterceptRequest(c.Request.URL.Path, cfg) {
			c.Next()
			return
		}

		storage, err := common.GetBodyStorage(c)
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] read body failed: %v", err))
			c.Next()
			return
		}

		body, err := storage.Bytes()
		if err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] read storage failed: %v", err))
			c.Next()
			return
		}

		var requestBody map[string]json.RawMessage
		if err := common.Unmarshal(body, &requestBody); err != nil {
			common.SysError(fmt.Sprintf("[ModelInterceptor] parse JSON failed: %v", err))
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

		category := DetermineCategory(originalModel, cfg)
		if category == "" {
			c.Next()
			return
		}

		if captureEnabled.Load() {
			recordCapture(originalModel, originalModel, category, "request", string(body), "")
		}

		ranker := GetModelRanker()
		triedModels := []string{}
		originalWriter := c.Writer
		var lastFailedRec *responseRecorder

		for {
			newModel := ranker.GetNextModel(category, triedModels)
			if newModel == "" {
				if len(triedModels) == 0 {
					common.SysLog(fmt.Sprintf("[ModelInterceptor] category %s no available models, passthrough", category))
					c.Writer = originalWriter
					c.Next()
					return
				}
				common.SysLog(fmt.Sprintf("[ModelInterceptor] category %s all models tried, returning last error (bodyLen=%d)", category, lastFailedRec.body.Len()))
				c.Writer = originalWriter
				if lastFailedRec != nil {
					lastFailedRec.FlushToOriginal()
				}
				return
			}

			triedModels = append(triedModels, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] model replace: %s -> %s (category: %s, attempt: %d/%d)", originalModel, newModel, category, len(triedModels), ranker.GetCategoryModelCount(category)))

			parsedBody := make(map[string]json.RawMessage)
			if err := common.Unmarshal(body, &parsedBody); err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] parse body failed: %v", err))
				c.Writer = originalWriter
				c.Next()
				return
			}

			parsedBody["model"], _ = common.Marshal(newModel)
			newBody, err := common.Marshal(parsedBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] marshal failed: %v", err))
				c.Writer = originalWriter
				c.Next()
				return
			}

			newStorage, err := common.CreateBodyStorage(newBody)
			if err != nil {
				common.SysError(fmt.Sprintf("[ModelInterceptor] create storage failed: %v", err))
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
			common.SysLog(fmt.Sprintf("[ModelInterceptor] Set mapped_model=%s, bodyLen=%d", newModel, len(newBody)))

			if len(triedModels) > 1 {
				clearChannelContext(c)
			}

			rec := newResponseRecorder(originalWriter)
			c.Writer = rec

			c.Next()

			if rec.Status() < 400 {
				common.SysLog(fmt.Sprintf("[ModelInterceptor] Success: status=%d, bodyLen=%d", rec.statusCode, rec.body.Len()))
				rec.FlushToOriginal()
				ranker.RecordSuccess(category, newModel)
				common.SysLog(fmt.Sprintf("[ModelInterceptor] model %s request success", newModel))

				if captureEnabled.Load() {
					recordCapture(originalModel, newModel, category, "response", string(newBody), rec.body.String())
				}
				return
			}

			ranker.RecordFailure(category, newModel)
			common.SysLog(fmt.Sprintf("[ModelInterceptor] model %s request failed (status: %d, bodyLen=%d), trying next", newModel, rec.Status(), rec.body.Len()))

			if captureEnabled.Load() {
				recordCapture(originalModel, newModel, category, "error", string(newBody), rec.body.String())
			}

			lastFailedRec = rec
			c.Writer = originalWriter

			if len(triedModels) >= ranker.GetCategoryModelCount(category) {
				common.SysLog(fmt.Sprintf("[ModelInterceptor] category %s all models tried, returning last error (bodyLen=%d)", category, lastFailedRec.body.Len()))
				lastFailedRec.FlushToOriginal()
				return
			}
		}
	}
}
