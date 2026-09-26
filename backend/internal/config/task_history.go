package config

import (
	"database/sql"
	"strings"
	"time"
)

// TaskHistoryEntry 是一条任务终态留档（task_history 表的一行）。
// 与 tasks 表不同，这里只记录任务结束后的结果，供任务历史页回看。
type TaskHistoryEntry struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	ContainerID   int    `json:"container_id"`
	ContainerName string `json:"container_name"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	Stage         string `json:"stage,omitempty"`
	StageDetail   string `json:"stage_detail,omitempty"`
	Percent       int    `json:"percent"`
	User          string `json:"user,omitempty"`
	IP            string `json:"ip,omitempty"`
	UserAgent     string `json:"user_agent,omitempty"`
	CreatedAt     string `json:"created_at"`
	StartedAt     string `json:"started_at,omitempty"`
	EndedAt       string `json:"ended_at,omitempty"`
	DurationMs    int64  `json:"duration_ms"`
}

// TaskLogEntry 是任务过程日志的一条记录（task_logs 表的一行）。
type TaskLogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// taskHistoryNow 统一使用 UTC RFC3339 字符串存时间，跨节点时区无关。
func taskHistoryNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

const taskHistoryColumns = `id, type, container_id, container_name, status, error, stage, stage_detail,
	percent, user, ip, user_agent, created_at, started_at, ended_at, duration_ms`

// scanTaskHistoryRow 按 taskHistoryColumns 的顺序读取一行。
func scanTaskHistoryRow(scan func(dest ...interface{}) error) (TaskHistoryEntry, error) {
	var e TaskHistoryEntry
	err := scan(&e.ID, &e.Type, &e.ContainerID, &e.ContainerName, &e.Status, &e.Error, &e.Stage,
		&e.StageDetail, &e.Percent, &e.User, &e.IP, &e.UserAgent, &e.CreatedAt, &e.StartedAt,
		&e.EndedAt, &e.DurationMs)
	return e, err
}

// UpsertTaskHistory 写入/更新一条任务终态留档（按 id 覆盖）。
// 数据库未初始化（如单元测试环境）时静默成功，避免影响任务主流程。
func UpsertTaskHistory(e TaskHistoryEntry) error {
	e.ID = strings.TrimSpace(e.ID)
	if e.ID == "" {
		return nil
	}
	if e.CreatedAt == "" {
		e.CreatedAt = taskHistoryNow()
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`INSERT OR REPLACE INTO task_history (`+taskHistoryColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Type, e.ContainerID, e.ContainerName, e.Status, e.Error, e.Stage, e.StageDetail,
		e.Percent, e.User, e.IP, e.UserAgent, e.CreatedAt, e.StartedAt, e.EndedAt, e.DurationMs)
	return err
}

// AppendTaskLog 追加一条任务日志。数据库未初始化时静默成功。
func AppendTaskLog(taskID, level, message string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	level = strings.TrimSpace(level)
	if level == "" {
		level = "INFO"
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`INSERT INTO task_logs (task_id, level, message, created_at) VALUES (?, ?, ?, ?)`,
		taskID, level, message, taskHistoryNow())
	return err
}

// ListTaskHistory 分页查询历史。statuses 为空表示不过滤；taskType/user 非空时按等值过滤；
// limit<=0 时默认 20，最大 100；offset<0 归一为 0。返回条目与过滤后总数。
func ListTaskHistory(statuses []string, taskType string, user string, limit, offset int) ([]TaskHistoryEntry, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	where := make([]string, 0, 3)
	args := make([]interface{}, 0, 3)
	if placeholders := make([]string, 0, len(statuses)); len(statuses) > 0 {
		for _, s := range statuses {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, s)
		}
		if len(placeholders) > 0 {
			where = append(where, "status IN ("+strings.Join(placeholders, ", ")+")")
		}
	}
	if t := strings.TrimSpace(taskType); t != "" {
		where = append(where, "type = ?")
		args = append(args, t)
	}
	if u := strings.TrimSpace(user); u != "" {
		where = append(where, "user = ?")
		args = append(args, u)
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return []TaskHistoryEntry{}, 0, nil
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_history`+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + taskHistoryColumns + ` FROM task_history` + clause +
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	rows, err := db.Query(query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []TaskHistoryEntry{}
	for rows.Next() {
		entry, err := scanTaskHistoryRow(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetTaskHistory 取单条历史；第二个返回值表示是否存在。
func GetTaskHistory(id string) (TaskHistoryEntry, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return TaskHistoryEntry{}, false, nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return TaskHistoryEntry{}, false, nil
	}
	entry, err := scanTaskHistoryRow(db.QueryRow(`SELECT `+taskHistoryColumns+` FROM task_history WHERE id = ?`, id).Scan)
	if err == sql.ErrNoRows {
		return TaskHistoryEntry{}, false, nil
	}
	if err != nil {
		return TaskHistoryEntry{}, false, err
	}
	return entry, true, nil
}

// ListTaskLogs 取某任务的全部日志（按 id ASC）。数据库未初始化时返回空列表。
func ListTaskLogs(taskID string) ([]TaskLogEntry, error) {
	taskID = strings.TrimSpace(taskID)
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil || taskID == "" {
		return []TaskLogEntry{}, nil
	}
	rows, err := db.Query(`SELECT level, message, created_at FROM task_logs WHERE task_id = ? ORDER BY id ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := []TaskLogEntry{}
	for rows.Next() {
		var l TaskLogEntry
		if err := rows.Scan(&l.Level, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

// TaskHistoryStats 统计：各状态计数、已完成任务平均耗时(ms)、按 type 分组的计数。
func TaskHistoryStats() (map[string]interface{}, error) {
	result := map[string]interface{}{
		"total":           0,
		"by_status":       map[string]int{},
		"by_type":         map[string]int{},
		"avg_duration_ms": int64(0),
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return result, nil
	}

	byStatus := map[string]int{}
	rows, err := db.Query(`SELECT status, COUNT(*) FROM task_history GROUP BY status`)
	if err != nil {
		return nil, err
	}
	total := 0
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return nil, err
		}
		byStatus[status] = count
		total += count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	byType := map[string]int{}
	typeRows, err := db.Query(`SELECT type, COUNT(*) FROM task_history GROUP BY type`)
	if err != nil {
		return nil, err
	}
	for typeRows.Next() {
		var taskType string
		var count int
		if err := typeRows.Scan(&taskType, &count); err != nil {
			typeRows.Close()
			return nil, err
		}
		byType[taskType] = count
	}
	typeRows.Close()
	if err := typeRows.Err(); err != nil {
		return nil, err
	}

	// 仅统计成功结束（completed）的任务平均耗时；无数据时 COALESCE 归零。
	var avgMs sql.NullFloat64
	if err := db.QueryRow(`SELECT AVG(duration_ms) FROM task_history WHERE status = 'completed'`).Scan(&avgMs); err != nil {
		return nil, err
	}
	if avgMs.Valid {
		result["avg_duration_ms"] = int64(avgMs.Float64)
	}
	result["total"] = total
	result["by_status"] = byStatus
	result["by_type"] = byType
	return result, nil
}

// PruneTaskHistory 保留最近 keep 条（按 created_at DESC, id DESC 排序），
// 删除更早的历史，并顺带清理不再有主记录的日志。返回删除的历史条数。
func PruneTaskHistory(keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return 0, nil
	}
	res, err := db.Exec(`DELETE FROM task_history WHERE id NOT IN (
		SELECT id FROM task_history ORDER BY created_at DESC, id DESC LIMIT ?)`, keep)
	if err != nil {
		return 0, err
	}
	deleted := 0
	if n, err := res.RowsAffected(); err == nil {
		deleted = int(n)
	}
	if _, err := db.Exec(`DELETE FROM task_logs WHERE task_id NOT IN (SELECT id FROM task_history)`); err != nil {
		return deleted, err
	}
	return deleted, nil
}
