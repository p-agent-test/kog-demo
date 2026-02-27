// Package monitor — SystemMonitor checks runtime health:
// heap memory usage, goroutine count, and (on Linux) disk usage.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"syscall"
	"time"
)

// SystemMonitor checks process-level and OS-level health.
type SystemMonitor struct {
	id     string
	logger *slog.Logger

	// Thresholds (defaults set in NewSystemMonitor).
	MaxAllocMB      float64 // heap alloc above this → warning
	MaxGoroutines   int     // goroutine count above this → warning
	MaxDiskUsePct   float64 // disk used% above this → warning
	DiskPath        string  // path to check disk usage on (default "/")
}

// NewSystemMonitor creates a SystemMonitor with sensible defaults.
func NewSystemMonitor(id string, logger *slog.Logger) *SystemMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &SystemMonitor{
		id:            id,
		logger:        logger,
		MaxAllocMB:    500,
		MaxGoroutines: 500,
		MaxDiskUsePct: 85,
		DiskPath:      "/",
	}
}

// ID implements Monitor.
func (s *SystemMonitor) ID() string { return s.id }

// Check implements Monitor. Inspects heap, goroutines, and disk.
func (s *SystemMonitor) Check(_ context.Context) ([]Observation, error) {
	var obs []Observation

	// --- Heap memory ---
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	allocMB := float64(ms.Alloc) / (1024 * 1024)

	if allocMB > s.MaxAllocMB {
		severity := SeverityWarning
		if allocMB > s.MaxAllocMB*1.5 {
			severity = SeverityError
		}
		obs = append(obs, Observation{
			MonitorID:   s.id,
			SituationID: "system_high_memory",
			Severity:    severity,
			Message:     fmt.Sprintf("High heap allocation: %.1f MB (threshold: %.0f MB)", allocMB, s.MaxAllocMB),
			Details: map[string]string{
				"alloc_mb":   fmt.Sprintf("%.2f", allocMB),
				"sys_mb":     fmt.Sprintf("%.2f", float64(ms.Sys)/(1024*1024)),
				"num_gc":     fmt.Sprintf("%d", ms.NumGC),
			},
			ObservedAt: time.Now().UTC(),
		})
	}

	// --- Goroutine count ---
	goroutines := runtime.NumGoroutine()
	if goroutines > s.MaxGoroutines {
		severity := SeverityWarning
		if goroutines > s.MaxGoroutines*2 {
			severity = SeverityError
		}
		obs = append(obs, Observation{
			MonitorID:   s.id,
			SituationID: "system_goroutine_leak",
			Severity:    severity,
			Message:     fmt.Sprintf("High goroutine count: %d (threshold: %d)", goroutines, s.MaxGoroutines),
			Details: map[string]string{
				"goroutines": fmt.Sprintf("%d", goroutines),
				"threshold":  fmt.Sprintf("%d", s.MaxGoroutines),
			},
			ObservedAt: time.Now().UTC(),
		})
	}

	// --- Disk usage (Linux/Darwin via syscall.Statfs) ---
	diskObs, err := s.checkDisk()
	if err != nil {
		s.logger.Debug("disk check skipped", "err", err)
	} else if diskObs != nil {
		obs = append(obs, *diskObs)
	}

	return obs, nil
}

// checkDisk returns an observation if disk usage exceeds the threshold.
func (s *SystemMonitor) checkDisk() (*Observation, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.DiskPath, &stat); err != nil {
		// Not supported on all platforms; skip silently.
		return nil, err
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total == 0 {
		return nil, nil
	}

	usedPct := float64(total-free) / float64(total) * 100
	usedGB := float64(total-free) / (1024 * 1024 * 1024)
	totalGB := float64(total) / (1024 * 1024 * 1024)

	if usedPct < s.MaxDiskUsePct {
		return nil, nil
	}

	// Log an info even when we check, for observability.
	s.logger.Debug("disk check", "path", s.DiskPath,
		"used_pct", fmt.Sprintf("%.1f%%", usedPct),
		"used_gb", fmt.Sprintf("%.1f", usedGB))

	severity := SeverityWarning
	if usedPct > 95 {
		severity = SeverityCritical
	} else if usedPct > 90 {
		severity = SeverityError
	}

	// Check if path actually exists (avoid false alarms on containers).
	if _, err := os.Stat(s.DiskPath); err != nil {
		return nil, err
	}

	return &Observation{
		MonitorID:   s.id,
		SituationID: "system_disk_high",
		Severity:    severity,
		Message: fmt.Sprintf("Disk usage %.1f%% (%.1f/%.1f GB) on %s",
			usedPct, usedGB, totalGB, s.DiskPath),
		Details: map[string]string{
			"path":      s.DiskPath,
			"used_pct":  fmt.Sprintf("%.1f", usedPct),
			"used_gb":   fmt.Sprintf("%.2f", usedGB),
			"total_gb":  fmt.Sprintf("%.2f", totalGB),
		},
		ObservedAt: time.Now().UTC(),
	}, nil
}
