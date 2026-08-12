package openlist

import (
	"io"
	"sync"
	"time"
)

// RateLimitedReader 限速读取器
// 通过滑动窗口算法限制读取速度，确保不超过指定的字节/秒速率
// limitBytesPerSec <= 0 表示不限速
type RateLimitedReader struct {
	reader    io.Reader
	limit     int64 // bytes per second
	read      int64 // total bytes read
	startTime time.Time
	mu        sync.Mutex
}

// NewRateLimitedReader 创建限速读取器
func NewRateLimitedReader(reader io.Reader, limitBytesPerSec int64) *RateLimitedReader {
	return &RateLimitedReader{
		reader:    reader,
		limit:     limitBytesPerSec,
		startTime: time.Now(),
	}
}

// Read 实现 io.Reader 接口
func (r *RateLimitedReader) Read(p []byte) (n int, err error) {
	if r.limit <= 0 {
		return r.reader.Read(p)
	}

	r.mu.Lock()
	// 计算期望的读取字节数（基于已用时间和速率限制）
	elapsed := time.Since(r.startTime)
	expectedBytes := int64(float64(r.limit) * elapsed.Seconds())

	// 如果实际读取量超过期望值，休眠等待
	if r.read > expectedBytes {
		overBytes := r.read - expectedBytes
		sleepDuration := time.Duration(float64(overBytes) / float64(r.limit) * float64(time.Second))
		// 限制单次休眠时间，避免长时间阻塞
		// 同时设置最小休眠阈值，避免过于频繁的微小休眠
		if sleepDuration > 100*time.Millisecond {
			r.mu.Unlock()
			time.Sleep(sleepDuration)
			r.mu.Lock()
		}
	}

	// 执行实际读取（锁外读取可能更好，但这里保持简单）
	r.mu.Unlock()
	n, err = r.reader.Read(p)
	r.mu.Lock()
	r.read += int64(n)
	r.mu.Unlock()

	return n, err
}