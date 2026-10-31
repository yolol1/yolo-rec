//go:build !windows

package configs

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// PermissionDiagnostics 包含权限诊断信息
type PermissionDiagnostics struct {
	FilePath    string
	FileExists  bool
	CanRead     bool
	CanWrite    bool
	FileMode    os.FileMode
	OwnerUID    uint32
	OwnerGID    uint32
	CurrentUID  int
	CurrentGID  int
	Suggestions []string
}

// DiagnoseFilePermission 诊断文件权限问题
func DiagnoseFilePermission(filePath string) *PermissionDiagnostics {
	diag := &PermissionDiagnostics{
		FilePath: filePath,
	}

	// 获取当前进程的 UID 和 GID
	diag.CurrentUID = os.Getuid()
	diag.CurrentGID = os.Getgid()

	// 检查文件是否存在
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		diag.FileExists = false
		diag.Suggestions = append(diag.Suggestions,
			fmt.Sprintf("文件 %s 不存在，请检查配置文件路径是否正确", filePath))
		return diag
	}
	if err != nil {
		diag.FileExists = false
		diag.Suggestions = append(diag.Suggestions,
			fmt.Sprintf("无法获取文件信息: %v", err))
		return diag
	}

	diag.FileExists = true
	diag.FileMode = fileInfo.Mode()

	// 获取文件所有者信息（仅 Unix 系统）
	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		diag.OwnerUID = stat.Uid
		diag.OwnerGID = stat.Gid
	}

	// 尝试读取文件
	file, err := os.OpenFile(filePath, os.O_RDONLY, 0)
	if err != nil {
		diag.CanRead = false
	} else {
		diag.CanRead = true
		file.Close()
	}

	// 尝试写入文件
	file, err = os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		diag.CanWrite = false
	} else {
		diag.CanWrite = true
		file.Close()
	}

	// 生成建议
	diag.generateSuggestions()

	return diag
}

// generateSuggestions 根据诊断结果生成建议
func (d *PermissionDiagnostics) generateSuggestions() {
	if d.CanRead && d.CanWrite {
		return // 没有权限问题
	}

	if !d.CanRead {
		d.Suggestions = append(d.Suggestions,
			fmt.Sprintf("无法读取文件 %s，请检查文件权限。当前权限: %v",
				d.FilePath, d.FileMode))
	}
	if !d.CanWrite {
		d.Suggestions = append(d.Suggestions,
			fmt.Sprintf("无法写入文件 %s，配置变更将无法保存。当前权限: %v",
				d.FilePath, d.FileMode))
	}
}

// FormatError 格式化权限诊断为用户友好的错误信息
func (d *PermissionDiagnostics) FormatError() string {
	if len(d.Suggestions) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n========== 权限诊断信息 ==========\n")

	for _, suggestion := range d.Suggestions {
		sb.WriteString(suggestion)
		sb.WriteString("\n")
	}

	sb.WriteString("===================================\n")
	return sb.String()
}
