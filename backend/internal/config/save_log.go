package config

// save_log.go —— 配置落库失败的可见化。
//
// 背景（P0-3 静默失败）：全库原有 22 处写成 `SaveConfigLogged()`、9 处
// `SaveConfigToDBLogged()`。落库失败被静默吞掉，表现为：用户在界面上改完设置、
// 接口返回 200、但改动没进数据库，重启后凭空消失，且没有任何线索可查。
//
// 这些调用点大多无法向上抛错（调用方没有可回滚的上下文），所以最低要求是
// **失败必留痕**，并带调用位置方便定位。

import (
	"log"
	"path/filepath"
	"runtime"
)

// SaveConfigLogged 落库配置；失败时记录带调用位置的日志，不改变控制流。
func SaveConfigLogged() {
	if err := SaveConfig(); err != nil {
		logSaveFailure(err, 2)
	}
}

// SaveConfigToDBLogged 同 SaveConfigLogged，但不做锁包装——
// 供已经持有 AppConfigMu 的内部路径使用（避免重复加锁死锁）。
func SaveConfigToDBLogged() {
	if err := saveConfigToDB(); err != nil {
		logSaveFailure(err, 2)
	}
}

// logSaveFailure 记录落库失败并标注调用位置。skip 为调用栈上跳层数。
func logSaveFailure(err error, skip int) {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		file, line = "?", 0
	}
	log.Printf("ERROR: 配置落库失败，本次改动可能丢失 (%s:%d): %v",
		filepath.Base(file), line, err)
}
