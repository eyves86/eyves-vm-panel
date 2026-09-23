package storage

import "time"

// strconvInt64NanoNow 返回当前 Unix 纳秒时间戳，被 storage 子包用来生成
// 临时 snapshot 唯一后缀。拆成函数便于测试 fake 时间。
func strconvInt64NanoNow() int64 {
	return time.Now().UnixNano()
}