package controller

import (
	"embed"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/relay"
	"github.com/gin-gonic/gin"
)

//go:embed model_rank.html
var modelRankHTML embed.FS

//go:embed capture.html
var captureHTML embed.FS

func GetModelRankStatus(c *gin.Context) {
	ranker := relay.GetModelRanker()
	status := ranker.GetRankStatus()
	cfg := relay.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
		"config": gin.H{
			"enabled":            cfg.Enabled,
			"score_config":       cfg.ScoreConfig,
			"intercept_endpoints": cfg.InterceptEndpoints,
		},
	})
}

func AddModelToRank(c *gin.Context) {
	var req struct {
		Category     string  `json:"category"`
		Model        string  `json:"model"`
		InitialScore float64 `json:"initial_score"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	ranker := relay.GetModelRanker()
	ranker.AddModel(req.Category, req.Model, req.InitialScore)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model added successfully",
	})
}

func RemoveModelFromRank(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
		Model    string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid request",
		})
		return
	}

	ranker := relay.GetModelRanker()
	ranker.RemoveModel(req.Category, req.Model)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model removed successfully",
	})
}

func GetModelRankPage(c *gin.Context) {
	data, err := modelRankHTML.ReadFile("model_rank.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load page")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func AddCategory(c *gin.Context) {
	var req struct {
		Category string   `json:"category"`
		Patterns []string `json:"patterns"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	err := relay.AddCategoryToConfig(req.Category, req.Patterns)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Category added successfully"})
}

func RemoveCategory(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	err := relay.RemoveCategoryFromConfig(req.Category)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Category removed successfully"})
}

func AddModelToConfig(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
		Model    string `json:"model"`
		Weight   int    `json:"weight"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	if req.Weight == 0 {
		req.Weight = 1
	}

	err := relay.AddModelToConfig(req.Category, req.Model, req.Weight)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Model added successfully"})
}

func RemoveModelFromConfig(c *gin.Context) {
	var req struct {
		Category string `json:"category"`
		Model    string `json:"model"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	err := relay.RemoveModelFromConfig(req.Category, req.Model)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Model removed successfully"})
}

func SaveConfig(c *gin.Context) {
	err := relay.SaveModelConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Config saved successfully"})
}

func ReloadConfig(c *gin.Context) {
	_, err := relay.ReloadModelConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Config reloaded successfully"})
}

func GetInterceptorMode(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"intercept_enabled": relay.IsInterceptorEnabled(),
			"capture_enabled":   relay.IsCaptureEnabled(),
		},
	})
}

func SetInterceptorMode(c *gin.Context) {
	var req struct {
		InterceptEnabled *bool `json:"intercept_enabled"`
		CaptureEnabled   *bool `json:"capture_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	if req.InterceptEnabled != nil {
		relay.SetInterceptorEnabled(*req.InterceptEnabled)
	}
	if req.CaptureEnabled != nil {
		relay.SetCaptureEnabled(*req.CaptureEnabled)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"intercept_enabled": relay.IsInterceptorEnabled(),
			"capture_enabled":   relay.IsCaptureEnabled(),
		},
	})
}

func GetCaptureRecords(c *gin.Context) {
	modelFilter := c.Query("model")
	limit := 50
	offset := 0
	if l, ok := c.GetQuery("limit"); ok {
		if v, err := parseInt(l); err == nil && v > 0 {
			limit = v
		}
	}
	if o, ok := c.GetQuery("offset"); ok {
		if v, err := parseInt(o); err == nil && v >= 0 {
			offset = v
		}
	}

	records, total, err := relay.GetCaptureRecords(modelFilter, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"records": records,
			"total":   total,
			"limit":   limit,
			"offset":  offset,
		},
	})
}

func DeleteCaptureRecords(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
		return
	}

	err := relay.DeleteCaptureRecords(req.IDs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Records deleted successfully"})
}

func DeleteAllCaptureRecords(c *gin.Context) {
	err := relay.DeleteAllCaptureRecords()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "All records deleted successfully"})
}

func GetCapturePage(c *gin.Context) {
	data, err := captureHTML.ReadFile("capture.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "Failed to load page")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func parseInt(s string) (int, error) {
	var v int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid integer")
		}
		v = v*10 + int(c-'0')
	}
	return v, nil
}
