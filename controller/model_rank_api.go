package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/relay"
	"github.com/gin-gonic/gin"
)

func GetModelRankStatus(c *gin.Context) {
	ranker := relay.GetModelRanker()
	status := ranker.GetRankStatus()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    status,
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
	c.HTML(http.StatusOK, "", nil)
}
