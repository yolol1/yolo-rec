package tools

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 回归用例：临时执行目录只应在 Linux 上启用。
// 历史 bug：路径被硬编码为 /opt/bililive/tmp_for_exec，在 Windows 上会被解析为
// 当前盘符根目录下的 \opt\bililive\tmp_for_exec（例如 D:\opt\bililive\tmp_for_exec），
// 导致每次启动都在用户磁盘上创建一个无用目录。
func TestResolveTmpExecRootFolder(t *testing.T) {
	if runtime.GOOS != "linux" {
		if got := resolveTmpExecRootFolder(); got != "" {
			t.Fatalf("非 Linux 平台不应设置临时执行目录，实际返回 %q", got)
		}
		return
	}

	t.Setenv("IS_DOCKER", "true")
	if got := resolveTmpExecRootFolder(); got != "/opt/bililive/tmp_for_exec" {
		t.Fatalf("容器内应沿用 /opt/bililive/tmp_for_exec，实际返回 %q", got)
	}

	t.Setenv("IS_DOCKER", "")
	got := resolveTmpExecRootFolder()
	if !filepath.IsAbs(got) {
		t.Fatalf("非容器 Linux 应返回绝对路径，实际返回 %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join("bililive-go", "tmp_for_exec")) {
		t.Fatalf("非容器 Linux 应返回系统临时目录下的路径，实际返回 %q", got)
	}
}
