package auth

import (
	"testing"
	"time"
)

func TestFailureLimiterDefaultsClock(t *testing.T) {
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 100, nil)
	if limiter.now == nil {
		t.Fatal("传入 nil 时应使用默认时钟")
	}
}

func TestFailureLimiterNormalizesNonPositiveCapacity(t *testing.T) {
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 0, time.Now)
	if limiter.maxEntries != 1 {
		t.Fatalf("非正容量应规范化为 1，实际为 %d", limiter.maxEntries)
	}
}

func TestFailureLimiterPanicsForNonPositiveMaxFailures(t *testing.T) {
	requirePanic(t, func() {
		NewFailureLimiter(0, time.Hour, 15*time.Minute, 100, time.Now)
	})
}

func TestFailureLimiterPanicsForNonPositiveWindow(t *testing.T) {
	requirePanic(t, func() {
		NewFailureLimiter(5, 0, 15*time.Minute, 100, time.Now)
	})
}

func TestFailureLimiterPanicsForNonPositiveBlockDuration(t *testing.T) {
	requirePanic(t, func() {
		NewFailureLimiter(5, time.Hour, 0, 100, time.Now)
	})
}

func TestFailureLimiterBlocksAtThreshold(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 100, func() time.Time {
		return now
	})

	for attempt := 1; attempt <= 4; attempt++ {
		blocked, retryAfter := limiter.RecordFailure("user-1")
		if blocked {
			t.Fatalf("第 %d 次失败不应触发阻塞", attempt)
		}
		if retryAfter != 0 {
			t.Fatalf("第 %d 次失败的剩余阻塞时间 = %v，期望 0", attempt, retryAfter)
		}
	}

	blocked, retryAfter := limiter.RecordFailure("user-1")
	if !blocked {
		t.Fatal("第 5 次失败应触发阻塞")
	}
	if retryAfter != 15*time.Minute {
		t.Fatalf("第 5 次失败的剩余阻塞时间 = %v，期望 %v", retryAfter, 15*time.Minute)
	}

	allowed, retryAfter := limiter.Allow("user-1")
	if allowed {
		t.Fatal("达到失败阈值后不应允许登录")
	}
	if retryAfter != 15*time.Minute {
		t.Fatalf("刚阻塞时的剩余时间 = %v，期望 %v", retryAfter, 15*time.Minute)
	}

	now = now.Add(5 * time.Minute)
	allowed, retryAfter = limiter.Allow("user-1")
	if allowed {
		t.Fatal("阻塞期内不应允许登录")
	}
	if retryAfter != 10*time.Minute {
		t.Fatalf("阻塞五分钟后的剩余时间 = %v，期望 %v", retryAfter, 10*time.Minute)
	}
}

func TestFailureLimiterSuccessClearsFailures(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(2, time.Hour, 15*time.Minute, 100, func() time.Time {
		return now
	})

	blocked, _ := limiter.RecordFailure("user-1")
	if blocked {
		t.Fatal("第一次失败不应触发阻塞")
	}

	limiter.Success("user-1")
	if _, ok := limiter.entries["user-1"]; ok {
		t.Fatal("登录成功后应删除失败条目")
	}

	blocked, _ = limiter.RecordFailure("user-1")
	if blocked {
		t.Fatal("登录成功后的第一次失败不应继承此前计数")
	}
	blocked, _ = limiter.RecordFailure("user-1")
	if !blocked {
		t.Fatal("登录成功后的第二次失败应重新达到阈值")
	}
}

func TestFailureLimiterAllowExpiresFailureWindow(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 100, func() time.Time {
		return now
	})

	for attempt := 1; attempt <= 4; attempt++ {
		limiter.RecordFailure("user-1")
	}
	now = now.Add(time.Hour)

	allowed, retryAfter := limiter.Allow("user-1")
	if !allowed {
		t.Fatal("失败窗口过期后应允许登录")
	}
	if retryAfter != 0 {
		t.Fatalf("失败窗口过期后的剩余阻塞时间 = %v，期望 0", retryAfter)
	}
	if _, ok := limiter.entries["user-1"]; ok {
		t.Fatal("失败窗口过期后应删除旧条目")
	}
}

func TestFailureLimiterRecordFailureStartsNewWindowAfterExpiry(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 100, func() time.Time {
		return now
	})

	for attempt := 1; attempt <= 4; attempt++ {
		limiter.RecordFailure("user-1")
	}
	now = now.Add(time.Hour)

	blocked, retryAfter := limiter.RecordFailure("user-1")
	if blocked {
		t.Fatal("新失败窗口的第一次失败不应触发阻塞")
	}
	if retryAfter != 0 {
		t.Fatalf("新失败窗口第一次失败的剩余阻塞时间 = %v，期望 0", retryAfter)
	}
	entry := limiter.entries["user-1"]
	if entry.Failures != 1 {
		t.Fatalf("新失败窗口的失败次数 = %d，期望 1", entry.Failures)
	}
	if !entry.WindowStart.Equal(now) {
		t.Fatalf("新失败窗口开始时间 = %v，期望 %v", entry.WindowStart, now)
	}
}

func TestFailureLimiterEvictsOldestEntryAtCapacity(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(5, time.Hour, 15*time.Minute, 2, func() time.Time {
		return now
	})

	limiter.RecordFailure("user-1")
	now = now.Add(time.Minute)
	limiter.RecordFailure("user-2")
	now = now.Add(time.Minute)
	limiter.RecordFailure("user-3")

	if len(limiter.entries) != 2 {
		t.Fatalf("容量为 2 时插入三个键后的条目数 = %d，期望 2", len(limiter.entries))
	}
	if _, ok := limiter.entries["user-1"]; ok {
		t.Fatal("达到容量时应淘汰最早访问的条目")
	}
	if _, ok := limiter.entries["user-2"]; !ok {
		t.Fatal("不应淘汰较新的 user-2 条目")
	}
	if _, ok := limiter.entries["user-3"]; !ok {
		t.Fatal("应保留新插入的 user-3 条目")
	}
}

func TestFailureLimiterAllowRefreshesBlockedEntryLastSeen(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(1, time.Hour, time.Hour, 2, func() time.Time {
		return now
	})

	limiter.RecordFailure("first")
	now = now.Add(time.Minute)
	limiter.RecordFailure("second")
	now = now.Add(time.Minute)
	allowed, _ := limiter.Allow("first")
	if allowed {
		t.Fatal("first 仍在阻塞期内，不应允许登录")
	}
	now = now.Add(time.Minute)
	limiter.RecordFailure("third")

	if _, ok := limiter.entries["first"]; !ok {
		t.Fatal("刚通过 Allow 访问的 first 应被保留")
	}
	if _, ok := limiter.entries["second"]; ok {
		t.Fatal("应淘汰 LastSeen 更早的 second")
	}
	if _, ok := limiter.entries["third"]; !ok {
		t.Fatal("应保留新插入的 third")
	}
}

func TestFailureLimiterEvictsStaleEntriesButKeepsActiveBlocks(t *testing.T) {
	now := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	limiter := NewFailureLimiter(2, 30*time.Minute, 2*time.Hour, 10, func() time.Time {
		return now
	})

	limiter.RecordFailure("stale")
	limiter.RecordFailure("blocked")
	limiter.RecordFailure("blocked")
	now = now.Add(time.Hour)
	limiter.RecordFailure("fresh")

	if _, ok := limiter.entries["stale"]; ok {
		t.Fatal("超过失败窗口且未阻塞的旧条目应被清理")
	}
	if _, ok := limiter.entries["blocked"]; !ok {
		t.Fatal("仍处于阻塞期的条目不应被清理")
	}
	if _, ok := limiter.entries["fresh"]; !ok {
		t.Fatal("应保留新插入的条目")
	}
}

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("期望发生 panic")
		}
	}()
	fn()
}
