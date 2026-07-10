package auth

import (
	"sync"
	"time"
)

type failureEntry struct {
	Failures     int
	WindowStart  time.Time
	BlockedUntil time.Time
	LastSeen     time.Time
}

// FailureLimiter 限制同一键在时间窗口内的连续登录失败。
type FailureLimiter struct {
	mu          sync.Mutex
	entries     map[string]failureEntry
	maxFailures int
	window      time.Duration
	blockFor    time.Duration
	maxEntries  int
	now         func() time.Time
}

// NewFailureLimiter 创建登录失败限流器。
func NewFailureLimiter(maxFailures int, window, blockFor time.Duration, maxEntries int, now func() time.Time) *FailureLimiter {
	if maxEntries < 1 {
		maxEntries = 1
	}
	if now == nil {
		now = time.Now
	}

	return &FailureLimiter{
		entries:     make(map[string]failureEntry),
		maxFailures: maxFailures,
		window:      window,
		blockFor:    blockFor,
		maxEntries:  maxEntries,
		now:         now,
	}
}

// Allow 返回指定键当前是否允许尝试登录及剩余阻塞时间。
func (l *FailureLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.entries[key]
	if !ok {
		return true, 0
	}
	if now.Before(entry.BlockedUntil) {
		return false, entry.BlockedUntil.Sub(now)
	}
	if !now.Before(entry.WindowStart.Add(l.window)) {
		delete(l.entries, key)
		return true, 0
	}
	return true, 0
}

// RecordFailure 记录一次登录失败并返回是否已阻塞及剩余阻塞时间。
func (l *FailureLimiter) RecordFailure(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry, ok := l.entries[key]
	if ok {
		delete(l.entries, key)
	}
	l.evict(now)
	if !ok {
		entry.WindowStart = now
	} else if !now.Before(entry.WindowStart.Add(l.window)) {
		blockedUntil := entry.BlockedUntil
		entry = failureEntry{WindowStart: now}
		if now.Before(blockedUntil) {
			entry.BlockedUntil = blockedUntil
		}
	}
	entry.Failures++
	entry.LastSeen = now
	if entry.Failures >= l.maxFailures {
		entry.BlockedUntil = now.Add(l.blockFor)
	}
	l.entries[key] = entry

	if now.Before(entry.BlockedUntil) {
		return true, entry.BlockedUntil.Sub(now)
	}
	return false, 0
}

// Success 清除指定键的失败记录。
func (l *FailureLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.entries, key)
}

func (l *FailureLimiter) evict(now time.Time) {
	for key, entry := range l.entries {
		blockEnded := !now.Before(entry.BlockedUntil)
		stale := now.Sub(entry.LastSeen) >= l.window
		if blockEnded && stale {
			delete(l.entries, key)
		}
	}

	if l.maxEntries < 1 {
		return
	}
	for len(l.entries) >= l.maxEntries {
		var oldestKey string
		var oldestSeen time.Time
		found := false
		for key, entry := range l.entries {
			if !found || entry.LastSeen.Before(oldestSeen) {
				oldestKey = key
				oldestSeen = entry.LastSeen
				found = true
			}
		}
		if !found {
			return
		}
		delete(l.entries, oldestKey)
	}
}
