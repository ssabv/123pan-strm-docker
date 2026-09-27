package app

import (
	"testing"
	"time"
)

// 回归：播放失败清缓存重登的 1 分钟限流
func TestTokenResetThrottled(t *testing.T) {
	if tokenResetThrottled(map[string]any{}) {
		t.Fatal("无记录不应限流")
	}
	if tokenResetThrottled(map[string]any{"lastTokenResetTime": float64(0)}) {
		t.Fatal("时间戳为 0(很久以前)不应限流")
	}
	now := float64(time.Now().Unix())
	if !tokenResetThrottled(map[string]any{"lastTokenResetTime": now}) {
		t.Fatal("刚刚触发过应限流")
	}
	if tokenResetThrottled(map[string]any{"lastTokenResetTime": now - 61}) {
		t.Fatal("超过 1 分钟不应限流")
	}
	// 恰好 59 秒: 限流
	if !tokenResetThrottled(map[string]any{"lastTokenResetTime": now - 59}) {
		t.Fatal("59 秒前触发应仍在限流窗口内")
	}
}
