package relay

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type CaptureRecord struct {
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	OriginalModel string `json:"original_model"`
	MappedModel  string `json:"mapped_model"`
	Category     string `json:"category"`
	Type         string `json:"type"`
	RequestBody  string `json:"request_body"`
	ResponseBody string `json:"response_body"`
}

var (
	captureMu     sync.Mutex
	captureFileMu sync.Mutex
	captureDir    = "capture_logs"
)

func recordCapture(originalModel, mappedModel, category, captureType, requestBody, responseBody string) {
	now := time.Now()
	record := CaptureRecord{
		ID:            fmt.Sprintf("%d", now.UnixNano()),
		Timestamp:     now.Format("2006-01-02T15:04:05.000Z07:00"),
		OriginalModel: originalModel,
		MappedModel:   mappedModel,
		Category:      category,
		Type:          captureType,
		RequestBody:   truncateString(requestBody, 50000),
		ResponseBody:  truncateString(responseBody, 50000),
	}

	go writeCaptureRecord(record)
}

func writeCaptureRecord(record CaptureRecord) {
	captureFileMu.Lock()
	defer captureFileMu.Unlock()

	dir := getCaptureDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] create capture dir failed: %v", err))
		return
	}

	dateStr := time.Now().Format("2006-01-02")
	filePath := filepath.Join(dir, fmt.Sprintf("capture_%s.jsonl", dateStr))

	data, err := json.Marshal(record)
	if err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] marshal capture record failed: %v", err))
		return
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		common.SysError(fmt.Sprintf("[ModelInterceptor] open capture file failed: %v", err))
		return
	}
	defer f.Close()

	f.Write(data)
	f.Write([]byte("\n"))
}

func getCaptureDir() string {
	for _, path := range []string{captureDir, filepath.Join("/data", captureDir)} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return captureDir
}

func GetCaptureRecords(modelFilter string, limit int, offset int) ([]CaptureRecord, int, error) {
	captureMu.Lock()
	defer captureMu.Unlock()

	dir := getCaptureDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CaptureRecord{}, 0, nil
		}
		return nil, 0, err
	}

	var allRecords []CaptureRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "capture_") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var record CaptureRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				continue
			}
			if modelFilter != "" {
				if !strings.Contains(strings.ToLower(record.OriginalModel), strings.ToLower(modelFilter)) &&
					!strings.Contains(strings.ToLower(record.MappedModel), strings.ToLower(modelFilter)) {
					continue
				}
			}
			allRecords = append(allRecords, record)
		}
	}

	sort.Slice(allRecords, func(i, j int) bool {
		return allRecords[i].Timestamp > allRecords[j].Timestamp
	})

	total := len(allRecords)
	if offset >= total {
		return []CaptureRecord{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}

	return allRecords[offset:end], total, nil
}

func DeleteCaptureRecords(ids []string) error {
	captureMu.Lock()
	defer captureMu.Unlock()

	if len(ids) == 0 {
		return nil
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	dir := getCaptureDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "capture_") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var keptLines []string
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var record CaptureRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				keptLines = append(keptLines, line)
				continue
			}
			if !idSet[record.ID] {
				keptLines = append(keptLines, line)
			}
		}

		newData := strings.Join(keptLines, "\n") + "\n"
		if err := os.WriteFile(filePath, []byte(newData), 0644); err != nil {
			return err
		}
	}

	return nil
}

func DeleteAllCaptureRecords() error {
	captureMu.Lock()
	defer captureMu.Unlock()

	dir := getCaptureDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "capture_") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		filePath := filepath.Join(dir, entry.Name())
		os.Remove(filePath)
	}

	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
