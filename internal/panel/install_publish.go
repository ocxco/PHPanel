package panel

import (
	"context"
	"fmt"
	"os"
	"time"
)

func renameInstallDirectory(ctx context.Context, source, target string) error {
	return retryInstallRename(ctx, source, target, 10*time.Second, os.Rename)
}

// Executables can remain briefly open in Windows filesystem filters after
// extraction. Keep the validated staging directory intact while waiting.
func retryInstallRename(ctx context.Context, source, target string, limit time.Duration, rename func(string, string) error) error {
	deadline := time.Now().Add(limit)
	delay := 50 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Never replace an unexpected destination, including an empty directory.
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("安装目录已存在，未覆盖已有文件: %s", target)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("无法检查安装目录: %w", err)
		}
		err := rename(source, target)
		if err == nil {
			return nil
		}
		if !retryableInstallRename(err) {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("安装目录持续被占用或不可写，自动重试 %s 后仍失败，请稍后重试安装: %w", limit, err)
		}
		wait := min(delay, remaining)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, 500*time.Millisecond)
	}
}
