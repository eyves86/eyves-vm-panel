package config

import (
	"database/sql"
	"errors"
)

// MetricSample is a persisted per-container metric sample.
type MetricSample struct {
	TS        int64   `json:"ts"`
	CPU       float64 `json:"cpu"`
	Memory    float64 `json:"memory"`
	NetworkRx float64 `json:"network_rx"`
	NetworkTx float64 `json:"network_tx"`
	DiskRead  float64 `json:"disk_read"`
	DiskWrite float64 `json:"disk_write"`
}

// MetricHourlySample is a per-container hourly rollup used for long-term
// retention without unbounded raw-sample growth.
type MetricHourlySample struct {
	Hour      int64   `json:"hour"` // bucket = start of the hour (ms)
	Count     int     `json:"count"`
	AvgCPU    float64 `json:"avg_cpu"`
	MaxCPU    float64 `json:"max_cpu"`
	AvgMemory float64 `json:"avg_memory"`
	MaxMemory float64 `json:"max_memory"`
	AvgNetRx  float64 `json:"avg_network_rx"`
	AvgNetTx  float64 `json:"avg_network_tx"`
	AvgDiskR  float64 `json:"avg_disk_read"`
	AvgDiskW  float64 `json:"avg_disk_write"`
}

// HourBucket returns the start-of-hour bucket (in ms) for a timestamp.
func HourBucket(tsMs int64) int64 {
	return tsMs - (tsMs % (60 * 60 * 1000))
}

// SaveMetricSamples persists metric samples for a container key.
func SaveMetricSamples(containerKey string, samples []MetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return errors.New("database not initialized")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO container_metrics
		(container_key, ts, cpu, memory, network_rx, network_tx, disk_read, disk_write)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, s := range samples {
		if _, err := stmt.Exec(containerKey, s.TS, s.CPU, s.Memory, s.NetworkRx, s.NetworkTx, s.DiskRead, s.DiskWrite); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// LoadMetricSamples returns metric samples for a container key at or after sinceMs.
func LoadMetricSamples(containerKey string, sinceMs int64) ([]MetricSample, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT ts, cpu, memory, network_rx, network_tx, disk_read, disk_write
		FROM container_metrics WHERE container_key = ? AND ts >= ? ORDER BY ts ASC`, containerKey, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	samples := make([]MetricSample, 0, 256)
	for rows.Next() {
		var s MetricSample
		if err := rows.Scan(&s.TS, &s.CPU, &s.Memory, &s.NetworkRx, &s.NetworkTx, &s.DiskRead, &s.DiskWrite); err != nil {
			return nil, err
		}
		samples = append(samples, s)
	}
	return samples, rows.Err()
}

// PruneMetricSamples deletes samples older than olderThanMs.
func PruneMetricSamples(olderThanMs int64) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM container_metrics WHERE ts < ?`, olderThanMs)
	return err
}

// PruneMetricSamplesForContainers deletes samples whose container no longer exists.
func PruneMetricSamplesForContainers(validKeys map[string]bool) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	if len(validKeys) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(validKeys))
	placeholders := ""
	for key := range validKeys {
		if placeholders != "" {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, key)
	}
	query := `DELETE FROM container_metrics WHERE container_key NOT IN (` + placeholders + `)`
	_, err := db.Exec(query, args...)
	return err
}

// RollupMetricSamples aggregates raw samples older than the current hour into
// per-container hourly buckets and deletes the aggregated raw rows. This keeps
// raw per-sample history short while retaining long-term trends via rollups.
// 0/negative keepAllHours is not relevant here; raw samples older than
// aggregateUptoMs (inclusive) are aggregated and removed.
func RollupMetricSamples(aggregateUptoMs int64) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Find all distinct container keys.
	keys, err := selectContainerMetricKeys(tx)
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT INTO container_metrics_hourly
		(container_key, hour, count, avg_cpu, max_cpu, avg_memory, max_memory,
		 avg_network_rx, avg_network_tx, avg_disk_read, avg_disk_write)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(container_key, hour) DO UPDATE SET
			count = excluded.count, avg_cpu = excluded.avg_cpu, max_cpu = excluded.max_cpu,
			avg_memory = excluded.avg_memory, max_memory = excluded.max_memory,
			avg_network_rx = excluded.avg_network_rx, avg_network_tx = excluded.avg_network_tx,
			avg_disk_read = excluded.avg_disk_read, avg_disk_write = excluded.avg_disk_write`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, key := range keys {
		rows, err := tx.Query(`SELECT ts, cpu, memory, network_rx, network_tx, disk_read, disk_write
			FROM container_metrics WHERE container_key = ? AND ts <= ? ORDER BY ts ASC`, key, aggregateUptoMs)
		if err != nil {
			return err
		}
		type bucket struct {
			count            int
			sumCPU, maxCPU   float64
			sumMem, maxMem   float64
			sumRx, sumTx     float64
			sumDr, sumDw     float64
		}
		buckets := map[int64]*bucket{}
		var minTS int64
		for rows.Next() {
			var s MetricSample
			if err := rows.Scan(&s.TS, &s.CPU, &s.Memory, &s.NetworkRx, &s.NetworkTx, &s.DiskRead, &s.DiskWrite); err != nil {
				rows.Close()
				return err
			}
			if minTS == 0 || s.TS < minTS {
				minTS = s.TS
			}
			h := HourBucket(s.TS)
			b := buckets[h]
			if b == nil {
				b = &bucket{}
				buckets[h] = b
			}
			b.count++
			b.sumCPU += s.CPU
			if s.CPU > b.maxCPU {
				b.maxCPU = s.CPU
			}
			b.sumMem += s.Memory
			if s.Memory > b.maxMem {
				b.maxMem = s.Memory
			}
			b.sumRx += s.NetworkRx
			b.sumTx += s.NetworkTx
			b.sumDr += s.DiskRead
			b.sumDw += s.DiskWrite
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for h, b := range buckets {
			if b.count == 0 {
				continue
			}
			n := float64(b.count)
			if _, err := stmt.Exec(key, h, b.count,
				b.sumCPU/n, b.maxCPU,
				b.sumMem/n, b.maxMem,
				b.sumRx/n, b.sumTx/n,
				b.sumDr/n, b.sumDw/n); err != nil {
				return err
			}
		}

		if minTS > 0 {
			if _, err := tx.Exec(`DELETE FROM container_metrics WHERE container_key = ? AND ts <= ?`, key, aggregateUptoMs); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// LoadMetricHourly returns hourly rollups for a container key at or after sinceMs.
func LoadMetricHourly(containerKey string, sinceMs int64) ([]MetricHourlySample, error) {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT hour, count, avg_cpu, max_cpu, avg_memory, max_memory,
		avg_network_rx, avg_network_tx, avg_disk_read, avg_disk_write
		FROM container_metrics_hourly WHERE container_key = ? AND hour >= ? ORDER BY hour ASC`,
		containerKey, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MetricHourlySample, 0, 256)
	for rows.Next() {
		var s MetricHourlySample
		if err := rows.Scan(&s.Hour, &s.Count, &s.AvgCPU, &s.MaxCPU, &s.AvgMemory, &s.MaxMemory,
			&s.AvgNetRx, &s.AvgNetTx, &s.AvgDiskR, &s.AvgDiskW); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PruneMetricHourly deletes hourly rollups older than olderThanMs.
func PruneMetricHourly(olderThanMs int64) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`DELETE FROM container_metrics_hourly WHERE hour < ?`, olderThanMs)
	return err
}

func selectContainerMetricKeys(tx interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT container_key FROM container_metrics`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
