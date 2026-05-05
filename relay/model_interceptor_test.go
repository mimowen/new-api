package relay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGetConfig(t *testing.T) {
	cfg := GetConfig()
	if cfg == nil {
		t.Fatal("GetConfig returned nil")
	}
}

func TestSetInterceptorEnabled(t *testing.T) {
	SetInterceptorEnabled(true)
	if !IsInterceptorEnabled() {
		t.Fatal("expected interceptor enabled")
	}
	SetInterceptorEnabled(false)
	if IsInterceptorEnabled() {
		t.Fatal("expected interceptor disabled")
	}
	SetInterceptorEnabled(true)
}

func TestSetCaptureEnabled(t *testing.T) {
	SetCaptureEnabled(true)
	if !IsCaptureEnabled() {
		t.Fatal("expected capture enabled")
	}
	SetCaptureEnabled(false)
	if IsCaptureEnabled() {
		t.Fatal("expected capture disabled")
	}
}

func TestModelRankerAddRemoveModel(t *testing.T) {
	ranker := &ModelRanker{
		rankings: make(map[string][]*RankedModel),
	}

	ranker.AddModel("test_cat", "model-a", 100)
	ranker.AddModel("test_cat", "model-b", 100)

	count := ranker.GetCategoryModelCount("test_cat")
	if count != 2 {
		t.Fatalf("expected 2 models, got %d", count)
	}

	next := ranker.GetNextModel("test_cat", []string{})
	if next != "model-a" && next != "model-b" {
		t.Fatalf("expected model-a or model-b, got %s", next)
	}

	ranker.RemoveModel("test_cat", "model-a")
	count = ranker.GetCategoryModelCount("test_cat")
	if count != 1 {
		t.Fatalf("expected 1 model after removal, got %d", count)
	}
}

func TestModelRankerAddRemoveCategory(t *testing.T) {
	ranker := &ModelRanker{
		rankings: make(map[string][]*RankedModel),
	}

	ranker.AddCategory("cat1", []string{"pattern1"}, []ModelWeight{}, 100)
	count := ranker.GetCategoryModelCount("cat1")
	if count != 0 {
		t.Fatalf("expected 0 models in new category, got %d", count)
	}

	ranker.AddModel("cat1", "model-x", 100)
	count = ranker.GetCategoryModelCount("cat1")
	if count != 1 {
		t.Fatalf("expected 1 model, got %d", count)
	}

	ranker.RemoveCategory("cat1")
	count = ranker.GetCategoryModelCount("cat1")
	if count != 0 {
		t.Fatalf("expected 0 models after category removal, got %d", count)
	}
}

func TestModelRankerRecordSuccessFailure(t *testing.T) {
	ranker := &ModelRanker{
		rankings: make(map[string][]*RankedModel),
	}

	ranker.AddModel("cat", "model-a", 100)
	ranker.AddModel("cat", "model-b", 100)

	ranker.RecordSuccess("cat", "model-a")
	status := ranker.GetRankStatus()
	for _, m := range status["cat"].Models {
		if m.Model == "model-a" {
			if m.Successes != 1 {
				t.Fatalf("expected 1 success, got %d", m.Successes)
			}
			if m.Score <= 100 {
				t.Fatalf("expected score > 100 after success, got %f", m.Score)
			}
		}
	}

	ranker.RecordFailure("cat", "model-b")
	status = ranker.GetRankStatus()
	for _, m := range status["cat"].Models {
		if m.Model == "model-b" {
			if m.Failures != 1 {
				t.Fatalf("expected 1 failure, got %d", m.Failures)
			}
			if m.Score >= 100 {
				t.Fatalf("expected score < 100 after failure, got %f", m.Score)
			}
		}
	}
}

func TestModelRankerGetNextModelExclusion(t *testing.T) {
	ranker := &ModelRanker{
		rankings: make(map[string][]*RankedModel),
	}

	ranker.AddModel("cat", "model-a", 100)
	ranker.AddModel("cat", "model-b", 80)

	next := ranker.GetNextModel("cat", []string{"model-a"})
	if next != "model-b" {
		t.Fatalf("expected model-b when model-a excluded, got %s", next)
	}

	next = ranker.GetNextModel("cat", []string{"model-a", "model-b"})
	if next != "" {
		t.Fatalf("expected empty string when all excluded, got %s", next)
	}
}

func TestDetermineCategory(t *testing.T) {
	cfg := &ModelMappingConfig{
		Mappings: map[string]CategoryConfig{
			"default": {
				Patterns: []string{"default"},
			},
			"high_tier": {
				Patterns: []string{"gpt-4", "claude-3"},
			},
		},
	}

	tests := []struct {
		model    string
		expected string
	}{
		{"default", "default"},
		{"DEFAULT", "default"},
		{"gpt-4o", "high_tier"},
		{"claude-3-opus", "high_tier"},
		{"llama-3", ""},
	}

	for _, tt := range tests {
		result := DetermineCategory(tt.model, cfg)
		if result != tt.expected {
			t.Errorf("DetermineCategory(%s) = %s, expected %s", tt.model, result, tt.expected)
		}
	}
}

func TestShouldInterceptRequest(t *testing.T) {
	cfg := &ModelMappingConfig{
		InterceptEndpoints: []string{"/v1/chat/completions", "/v1/completions"},
	}

	tests := []struct {
		path     string
		expected bool
	}{
		{"/v1/chat/completions", true},
		{"/v1/completions", true},
		{"/v1/models", false},
		{"/api/status", false},
	}

	for _, tt := range tests {
		result := shouldInterceptRequest(tt.path, cfg)
		if result != tt.expected {
			t.Errorf("shouldInterceptRequest(%s) = %v, expected %v", tt.path, result, tt.expected)
		}
	}
}

func TestAddCategoryToConfig(t *testing.T) {
	cfg := &ModelMappingConfig{
		Enabled:  true,
		Mappings: make(map[string]CategoryConfig),
	}
	configPtr.Store(cfg)

	err := AddCategoryToConfig("test_cat", []string{"test-pattern"})
	if err != nil {
		t.Fatalf("AddCategoryToConfig failed: %v", err)
	}

	newCfg := GetConfig()
	if _, ok := newCfg.Mappings["test_cat"]; !ok {
		t.Fatal("category not found in config after add")
	}

	err = AddCategoryToConfig("test_cat", []string{"dup"})
	if err == nil {
		t.Fatal("expected error for duplicate category")
	}

	delete(newCfg.Mappings, "test_cat")
	configPtr.Store(newCfg)
}

func TestAddRemoveModelToConfig(t *testing.T) {
	cfg := &ModelMappingConfig{
		Enabled: true,
		Mappings: map[string]CategoryConfig{
			"test_cat": {Models: []ModelWeight{}},
		},
	}
	configPtr.Store(cfg)

	err := AddModelToConfig("test_cat", "test-model", 1)
	if err != nil {
		t.Fatalf("AddModelToConfig failed: %v", err)
	}

	newCfg := GetConfig()
	found := false
	for _, m := range newCfg.Mappings["test_cat"].Models {
		if m.Model == "test-model" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("model not found in config after add")
	}

	err = RemoveModelFromConfig("test_cat", "test-model")
	if err != nil {
		t.Fatalf("RemoveModelFromConfig failed: %v", err)
	}

	err = RemoveModelFromConfig("test_cat", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}

func TestSaveAndReloadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "model_mapping.yaml")

	cfg := &ModelMappingConfig{
		Enabled: true,
		ScoreConfig: ScoreConfig{
			InitialScore:   50,
			SuccessBonus:   10,
			FailurePenalty: 20,
		},
		Mappings: map[string]CategoryConfig{
			"test_save": {
				Patterns: []string{"save-test"},
				Models:   []ModelWeight{{Model: "save-model", Weight: 1}},
			},
		},
		InterceptEndpoints: []string{"/v1/chat/completions"},
	}
	configPtr.Store(cfg)

	ranker := GetModelRanker()
	ranker.AddCategory("test_save", []string{"save-test"}, []ModelWeight{{Model: "save-model", Weight: 1}}, 50)

	data, err := yamlMarshal(cfg)
	if err != nil {
		t.Fatalf("yaml marshal failed: %v", err)
	}
	err = os.WriteFile(cfgPath, data, 0644)
	if err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	readData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}

	loadedCfg := &ModelMappingConfig{}
	err = json.Unmarshal(readData, loadedCfg)
	if err != nil {
		yamlUnmarshal(t, readData, loadedCfg)
	}

	if !loadedCfg.Enabled {
		t.Fatal("expected enabled=true in saved config")
	}
	if loadedCfg.Mappings["test_save"].Patterns[0] != "save-test" {
		t.Fatal("patterns not saved correctly")
	}
}

func yamlMarshal(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

func yamlUnmarshal(t *testing.T, data []byte, v interface{}) {
	t.Helper()
	err := yaml.Unmarshal(data, v)
	if err != nil {
		t.Fatalf("yaml unmarshal failed: %v", err)
	}
}

func TestConcurrentConfigAccess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = GetConfig()
		}()
		go func() {
			defer wg.Done()
			SetInterceptorEnabled(true)
		}()
		go func() {
			defer wg.Done()
			_ = IsInterceptorEnabled()
		}()
	}
	wg.Wait()
}

func TestConcurrentRankerAccess(t *testing.T) {
	ranker := &ModelRanker{
		rankings: make(map[string][]*RankedModel),
	}
	ranker.AddModel("cat", "model-a", 100)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			ranker.RecordSuccess("cat", "model-a")
		}()
		go func() {
			defer wg.Done()
			ranker.RecordFailure("cat", "model-a")
		}()
		go func() {
			defer wg.Done()
			_ = ranker.GetNextModel("cat", []string{})
		}()
		go func() {
			defer wg.Done()
			_ = ranker.GetRankStatus()
		}()
	}
	wg.Wait()
}
