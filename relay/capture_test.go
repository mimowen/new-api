package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRecordAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	origDir := captureDir
	captureDir = tmpDir
	defer func() { captureDir = origDir }()

	record := CaptureRecord{
		ID:            "test-001",
		Timestamp:     "2026-05-03T12:00:00.000Z",
		OriginalModel: "default",
		MappedModel:   "gpt-4o",
		Category:      "default",
		Type:          "request",
		RequestBody:   `{"model":"default","messages":[]}`,
		ResponseBody:  "",
	}

	writeCaptureRecord(record)

	records, total, err := GetCaptureRecords("", 10, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 record, got %d", total)
	}
	if records[0].ID != "test-001" {
		t.Fatalf("expected ID test-001, got %s", records[0].ID)
	}
	if records[0].OriginalModel != "default" {
		t.Fatalf("expected original model 'default', got %s", records[0].OriginalModel)
	}
}

func TestCaptureFilterByModel(t *testing.T) {
	tmpDir := t.TempDir()
	origDir := captureDir
	captureDir = tmpDir
	defer func() { captureDir = origDir }()

	records := []CaptureRecord{
		{ID: "1", Timestamp: "2026-05-03T12:00:00Z", OriginalModel: "default", MappedModel: "gpt-4o", Category: "default", Type: "request"},
		{ID: "2", Timestamp: "2026-05-03T12:01:00Z", OriginalModel: "claude-3", MappedModel: "claude-3-opus", Category: "high_tier", Type: "request"},
		{ID: "3", Timestamp: "2026-05-03T12:02:00Z", OriginalModel: "default", MappedModel: "gpt-4o-mini", Category: "default", Type: "response"},
	}

	for _, r := range records {
		writeCaptureRecord(r)
	}

	filtered, total, err := GetCaptureRecords("gpt-4o", 10, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords with filter failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 filtered records, got %d", total)
	}

	all, totalAll, err := GetCaptureRecords("", 10, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords without filter failed: %v", err)
	}
	if totalAll != 3 {
		t.Fatalf("expected 3 total records, got %d", totalAll)
	}
	_ = filtered
	_ = all
}

func TestCaptureDeleteRecords(t *testing.T) {
	tmpDir := t.TempDir()
	origDir := captureDir
	captureDir = tmpDir
	defer func() { captureDir = origDir }()

	records := []CaptureRecord{
		{ID: "del-1", Timestamp: "2026-05-03T12:00:00Z", OriginalModel: "default", MappedModel: "gpt-4o", Category: "default", Type: "request"},
		{ID: "del-2", Timestamp: "2026-05-03T12:01:00Z", OriginalModel: "default", MappedModel: "gpt-4o", Category: "default", Type: "response"},
	}

	for _, r := range records {
		writeCaptureRecord(r)
	}

	err := DeleteCaptureRecords([]string{"del-1"})
	if err != nil {
		t.Fatalf("DeleteCaptureRecords failed: %v", err)
	}

	remaining, total, err := GetCaptureRecords("", 10, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords after delete failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 record after delete, got %d", total)
	}
	if remaining[0].ID != "del-2" {
		t.Fatalf("expected remaining ID del-2, got %s", remaining[0].ID)
	}
}

func TestCaptureDeleteAllRecords(t *testing.T) {
	tmpDir := t.TempDir()
	origDir := captureDir
	captureDir = tmpDir
	defer func() { captureDir = origDir }()

	records := []CaptureRecord{
		{ID: "all-1", Timestamp: "2026-05-03T12:00:00Z", OriginalModel: "default", MappedModel: "gpt-4o", Category: "default", Type: "request"},
		{ID: "all-2", Timestamp: "2026-05-03T12:01:00Z", OriginalModel: "default", MappedModel: "gpt-4o", Category: "default", Type: "response"},
	}

	for _, r := range records {
		writeCaptureRecord(r)
	}

	err := DeleteAllCaptureRecords()
	if err != nil {
		t.Fatalf("DeleteAllCaptureRecords failed: %v", err)
	}

	_, total, err := GetCaptureRecords("", 10, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords after delete all failed: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected 0 records after delete all, got %d", total)
	}
}

func TestCapturePagination(t *testing.T) {
	tmpDir := t.TempDir()
	origDir := captureDir
	captureDir = tmpDir
	defer func() { captureDir = origDir }()

	for i := 0; i < 5; i++ {
		record := CaptureRecord{
			ID:            fmt.Sprintf("page-%d", i),
			Timestamp:     fmt.Sprintf("2026-05-03T12:0%d:00Z", i),
			OriginalModel: "default",
			MappedModel:   "gpt-4o",
			Category:      "default",
			Type:          "request",
		}
		writeCaptureRecord(record)
	}

	page1, total, err := GetCaptureRecords("", 2, 0)
	if err != nil {
		t.Fatalf("GetCaptureRecords page 1 failed: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 records on page 1, got %d", len(page1))
	}

	page3, _, err := GetCaptureRecords("", 2, 4)
	if err != nil {
		t.Fatalf("GetCaptureRecords page 3 failed: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("expected 1 record on page 3, got %d", len(page3))
	}
}

func TestTruncateString(t *testing.T) {
	short := "hello"
	if truncateString(short, 10) != short {
		t.Fatal("short string should not be truncated")
	}

	long := strings.Repeat("a", 200)
	result := truncateString(long, 100)
	if len(result) > 120 {
		t.Fatalf("truncated string too long: %d", len(result))
	}
	if !strings.HasSuffix(result, "...[truncated]") {
		t.Fatal("truncated string should end with ...[truncated]")
	}
}

func TestGetCaptureDirFallback(t *testing.T) {
	dir := getCaptureDir()
	if dir == "" {
		t.Fatal("getCaptureDir should not return empty string")
	}

	tmpDir := t.TempDir()
	expected := filepath.Join(tmpDir, "capture_logs")
	os.MkdirAll(expected, 0755)

	origDir := captureDir
	captureDir = filepath.Join(tmpDir, "capture_logs")
	defer func() { captureDir = origDir }()

	result := getCaptureDir()
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}
