// Package ratelimit 为每个直播平台提供访问频率限制功能
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// PlatformRateLimiter 管理各个直播平台的访问频率限制
type PlatformRateLimiter struct {
	limiters map[string]*PlatformLimiter // 平台名称 -> 限制器
	mu       sync.RWMutex                // 读写锁保护map
}

// PlatformLimiter 单个平台的频率限制器
type PlatformLimiter struct {
	minInterval time.Duration // 最小访问间隔
	lastAccess  time.Time     // 上次访问时间
	mu          sync.Mutex    // 保护访问时间的互斥锁
}

var globalRateLimiter = &PlatformRateLimiter{
	limiters: make(map[string]*PlatformLimiter),
}

// GetGlobalRateLimiter 获取全局速率限制器实例
func GetGlobalRateLimiter() *PlatformRateLimiter {
	return globalRateLimiter
}

// SetPlatformLimit 设置或更新指定平台的访问频率限制
func (prl *PlatformRateLimiter) SetPlatformLimit(platform string, intervalSec int) {
	if intervalSec <= 0 {
		// 如果间隔为0或负数，移除该平台的限制
		prl.mu.Lock()
		delete(prl.limiters, platform)
		prl.mu.Unlock()
		return
	}

	interval := time.Duration(intervalSec) * time.Second

	prl.mu.Lock()
	defer prl.mu.Unlock()

	if limiter, exists := prl.limiters[platform]; exists {
		// 更新现有限制器的间隔
		limiter.mu.Lock()
		limiter.minInterval = interval
		limiter.mu.Unlock()
	} else {
		// 创建新的限制器
		prl.limiters[platform] = &PlatformLimiter{
			minInterval: interval,
			lastAccess:  time.Time{}, // 零值时间，首次访问不会被限制
		}
	}
}

// WaitForPlatform 等待直到允许访问指定平台
// 如果平台没有设置限制，立即返回
// 注意：此函数在等待期间不持有锁，以允许 ForceAccess 等操作可以随时执行
func (prl *PlatformRateLimiter) WaitForPlatform(platform string) {
	prl.WaitForPlatformWithContext(context.Background(), platform)
}

// WaitForPlatformWithContext 等待直到允许访问指定平台，支持 context 取消
// 如果平台没有设置限制，立即返回 true
// 返回 true 表示成功获取访问权限，false 表示被 context 取消
func (prl *PlatformRateLimiter) WaitForPlatformWithContext(ctx context.Context, platform string) bool {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()

	if !exists {
		// 平台没有设置限制，立即返回
		return true
	}

	// 检查 context 是否已取消 (fast path)
	select {
	case <-ctx.Done():
		return false
	default:
	}

	limiter.mu.Lock()
	now := time.Now()

	// 计算这个请求被分配的执行时间
	var allowedTime time.Time
	// limiter.lastAccess 实际上表示的是"上一个请求被分配的执行时间"
	nextAllowed := limiter.lastAccess.Add(limiter.minInterval)
	if nextAllowed.Before(now) {
		allowedTime = now
	} else {
		allowedTime = nextAllowed
	}
	limiter.lastAccess = allowedTime
	limiter.mu.Unlock()

	waitTime := allowedTime.Sub(now)
	if waitTime > 0 {
		timer := time.NewTimer(waitTime)
		select {
		case <-ctx.Done():
			timer.Stop()
			// 注意：如果被取消，这会导致预留的时间槽被浪费
			// 但这不影响正确性，只是可能在短时间内少发几个请求
			return false
		case <-timer.C:
			// 成功等待到了指定时间
		}
	}

	return true
}

// GetPlatformNextAllowedTime 获取平台下次允许访问的时间
func (prl *PlatformRateLimiter) GetPlatformNextAllowedTime(platform string) time.Time {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()

	if !exists {
		// 没有限制，立即可访问
		return time.Now()
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	return limiter.lastAccess.Add(limiter.minInterval)
}

// RemovePlatformLimit 移除指定平台的访问限制
func (prl *PlatformRateLimiter) RemovePlatformLimit(platform string) {
	prl.mu.Lock()
	defer prl.mu.Unlock()

	delete(prl.limiters, platform)
}

// GetAllPlatformLimits 获取所有平台的当前限制设置
func (prl *PlatformRateLimiter) GetAllPlatformLimits() map[string]int {
	prl.mu.RLock()
	defer prl.mu.RUnlock()

	limits := make(map[string]int)
	for platform, limiter := range prl.limiters {
		limiter.mu.Lock()
		limits[platform] = int(limiter.minInterval.Seconds())
		limiter.mu.Unlock()
	}

	return limits
}

// WaitInfo 包含平台等待状态信息
type WaitInfo struct {
	WaitedSeconds    float64 // 自上次请求以来已等待的秒数
	NextRequestInSec float64 // 预计多少秒后可以发送下一次请求（0 表示立即可以）
	MinIntervalSec   int     // 平台设置的最小访问间隔
}

// GetPlatformWaitInfo 获取指定平台的等待状态信息
func (prl *PlatformRateLimiter) GetPlatformWaitInfo(platform string) WaitInfo {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()

	if !exists {
		// 平台没有设置限制
		return WaitInfo{
			WaitedSeconds:    0,
			NextRequestInSec: 0,
			MinIntervalSec:   0,
		}
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	nextAllowed := limiter.lastAccess.Add(limiter.minInterval)

	var nextRequestIn float64
	if nextAllowed.After(now) {
		nextRequestIn = nextAllowed.Sub(now).Seconds()
	}

	return WaitInfo{
		WaitedSeconds:    0, // 不再全局追踪排队等待时间
		MinIntervalSec:   int(limiter.minInterval.Seconds()),
		NextRequestInSec: nextRequestIn,
	}
}

// ForceAccess 强制访问平台，忽略频率限制
// 返回距离上次访问的时间间隔
func (prl *PlatformRateLimiter) ForceAccess(platform string) time.Duration {
	prl.mu.RLock()
	limiter, exists := prl.limiters[platform]
	prl.mu.RUnlock()

	if !exists {
		return 0
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(limiter.lastAccess)
	
	// 如果上次预定的时间在未来，强制访问不改变排队队列，仅更新为当前时间（如果它在过去的话）
	if limiter.lastAccess.Before(now) {
		limiter.lastAccess = now
	}
	
	return elapsed
}
