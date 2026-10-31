package servers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/configs"
)

// getSafePath 校验路径不会逃逸出根目录，返回解析符号链接后的绝对路径。
//
// 仅比较字符串无法防止路径中的符号链接或 Windows junction 指向根目录之外，
// 因此这里同时校验词法路径和真实路径。目标尚不存在时，会解析最近的已存在父目录。
func getSafePath(base, subPath string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}

	resolvedBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", fmt.Errorf("解析根目录失败: %w", err)
	}

	target := subPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(resolvedBase, target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	if !pathWithinRoot(resolvedBase, absTarget) {
		return "", errors.New("非法路径访问：超出授权范围")
	}
	if err := ensureNoReparsePoints(resolvedBase, absTarget); err != nil {
		return "", err
	}

	resolvedTarget, err := resolvePathWithExistingParent(absTarget)
	if err != nil {
		return "", fmt.Errorf("解析目标路径失败: %w", err)
	}

	if !pathWithinRoot(resolvedBase, resolvedTarget) {
		return "", errors.New("非法路径访问：超出授权范围")
	}

	return resolvedTarget, nil
}

// ensureNoReparsePoints 拒绝输出目录内部的符号链接、junction 等重解析点。
// 录制目录不需要通过链接访问文件，直接拒绝可以避免检查与使用之间的语义差异。
func ensureNoReparsePoints(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		reparsePoint, err := isPathReparsePoint(current, info)
		if err != nil {
			return err
		}
		if reparsePoint {
			return errors.New("非法路径访问：禁止访问符号链接或 junction")
		}
	}
	return nil
}

// pathWithinRoot 判断 target 是否等于 root 或位于 root 之下。
func pathWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

func validateEntryName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return errors.New("文件名无效")
	}
	return nil
}

// resolvePathWithExistingParent 解析路径中的符号链接。
// 目标可以尚不存在，此时只解析最近的已存在父目录，再拼回不存在的尾部。
func resolvePathWithExistingParent(target string) (string, error) {
	current := filepath.Clean(target)
	var missingParts []string

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missingParts) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missingParts[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missingParts = append(missingParts, filepath.Base(current))
		current = parent
	}
}

func getFileInfo(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	cfg := configs.GetCurrentConfig()
	absPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "无效或越权路径",
		})
		return
	}

	files, err := os.ReadDir(absPath)
	if err != nil {
		writeJSON(writer, commonResp{
			ErrMsg: "获取目录失败",
		})
		return
	}

	type jsonFile struct {
		IsFolder     bool   `json:"is_folder"`
		Name         string `json:"name"`
		LastModified int64  `json:"last_modified"`
		Size         int64  `json:"size"`
		SubtitleFile string `json:"subtitle_file,omitempty"`
	}

	// 第一遍：分离 ASS 弹幕文件并建立 baseName -> ASS 文件名映射
	assFiles := make(map[string]string)
	type fileEntry struct {
		dir  os.DirEntry
		info os.FileInfo
	}
	var validFiles []fileEntry
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			continue
		}
		name := file.Name()
		if !file.IsDir() && strings.HasSuffix(strings.ToLower(name), ".ass") {
			baseName := name[:len(name)-4]
			assFiles[baseName] = name
		} else {
			validFiles = append(validFiles, fileEntry{dir: file, info: info})
		}
	}

	// 第二遍：构建响应，将弹幕信息关联到对应视频文件
	jsonFiles := make([]jsonFile, 0, len(validFiles))
	for _, fe := range validFiles {
		jf := jsonFile{
			IsFolder:     fe.dir.IsDir(),
			Name:         fe.dir.Name(),
			LastModified: fe.info.ModTime().Unix(),
		}
		if !fe.dir.IsDir() {
			jf.Size = fe.info.Size()
			baseName := fe.dir.Name()
			if idx := strings.LastIndex(baseName, "."); idx > 0 {
				baseName = baseName[:idx]
			}
			if assName, ok := assFiles[baseName]; ok {
				jf.SubtitleFile = assName
			}
		}
		jsonFiles = append(jsonFiles, jf)
	}

	json := struct {
		Files []jsonFile `json:"files"`
		Path  string     `json:"path"`
	}{
		Files: jsonFiles,
		Path:  path,
	}

	writeJSON(writer, json)
}

// newOutputFileHandler 提供只读文件访问，并复用与文件管理 API 相同的路径校验。
func newOutputFileHandler(base string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(filepath.ToSlash(r.URL.Path), "/")
		absPath, err := getSafePath(base, filepath.FromSlash(requestPath))
		if err != nil {
			http.NotFound(writer, r)
			return
		}
		http.ServeFile(writer, r, absPath)
	})
}

// translateOSError 将系统错误转换为中文，兼容多平台
func translateOSError(err error) string {
	if err == nil {
		return ""
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "操作失败：文件或文件夹不存在"
	case errors.Is(err, fs.ErrExist):
		return "操作失败：目标文件名已存在"
	case errors.Is(err, fs.ErrPermission):
		return "操作被拒绝：权限不足或文件/文件夹正被占用"
	}

	errStr := err.Error()
	loweredErr := strings.ToLower(errStr)
	switch {
	case strings.Contains(loweredErr, "being used by another process"),
		strings.Contains(loweredErr, "sharing violation"):
		return "操作失败：文件正被另一个程序占用"
	case strings.Contains(loweredErr, "is not empty"),
		strings.Contains(loweredErr, "directory not empty"):
		return "操作失败：目录不为空"
	default:
		return errStr
	}
}

func renameFile(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	var body struct {
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}
	if err := validateEntryName(body.NewName); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: err.Error()})
		return
	}

	cfg := configs.GetCurrentConfig()
	oldAbsPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效或越权路径"})
		return
	}

	info, err := os.Stat(oldAbsPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 404, ErrMsg: "文件不存在"})
		return
	}

	var newAbsPath string
	baseDir := filepath.Dir(oldAbsPath)
	if info.IsDir() {
		newAbsPath = filepath.Join(baseDir, body.NewName)
	} else {
		ext := filepath.Ext(oldAbsPath)
		newAbsPath = filepath.Join(baseDir, body.NewName+ext)
	}

	newAbsPath, err = getSafePath(cfg.OutPutPath, newAbsPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "非法的新文件名：禁止越界路径"})
		return
	}

	if _, err := os.Stat(newAbsPath); err == nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "重命名失败：目标文件名已存在"})
		return
	}

	if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "重命名失败: " + translateOSError(err)})
		return
	}

	// 同步重命名关联的 ASS 弹幕文件
	if !info.IsDir() {
		oldBase := strings.TrimSuffix(oldAbsPath, filepath.Ext(oldAbsPath))
		newBase := strings.TrimSuffix(newAbsPath, filepath.Ext(newAbsPath))
		oldAss := oldBase + ".ass"
		newAss := newBase + ".ass"
		if _, err := os.Stat(oldAss); err == nil {
			os.Rename(oldAss, newAss)
		}
	}

	writeJSON(writer, commonResp{Data: "OK"})
}

func deleteFile(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}
	absPath, err := getSafePath(cfg.OutPutPath, path)
	if err != nil || absPath == base {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "禁止删除根目录或无效/越权路径"})
		return
	}

	// 删除关联的 ASS 弹幕文件
	if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
		assPath := strings.TrimSuffix(absPath, filepath.Ext(absPath)) + ".ass"
		if _, err := os.Stat(assPath); err == nil {
			os.Remove(assPath)
		}
	}

	if err := os.RemoveAll(absPath); err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "删除失败: " + translateOSError(err)})
		return
	}

	writeJSON(writer, commonResp{Data: "OK"})
}

func batchRenameFiles(writer http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths   []string `json:"paths"`
		Find    string   `json:"find"`
		Replace string   `json:"replace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}

	cfg := configs.GetCurrentConfig()
	type Result struct {
		Path    string `json:"path"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	results := make([]Result, 0, len(body.Paths))

	for _, path := range body.Paths {
		oldAbsPath, err := getSafePath(cfg.OutPutPath, path)
		if err != nil {
			results = append(results, Result{Path: path, Success: false, Message: "无效或越权路径"})
			continue
		}

		info, err := os.Stat(oldAbsPath)
		if err != nil {
			results = append(results, Result{Path: path, Success: false, Message: "文件不存在"})
			continue
		}

		oldName := filepath.Base(oldAbsPath)
		var newName string
		if info.IsDir() {
			newName = strings.ReplaceAll(oldName, body.Find, body.Replace)
		} else {
			ext := filepath.Ext(oldName)
			nameWithoutExt := strings.TrimSuffix(oldName, ext)
			newNameWithoutExt := strings.ReplaceAll(nameWithoutExt, body.Find, body.Replace)
			newName = newNameWithoutExt + ext
		}

		if oldName == newName {
			results = append(results, Result{Path: path, Success: true, Message: "无需更改"})
			continue
		}
		if err := validateEntryName(newName); err != nil {
			results = append(results, Result{Path: path, Success: false, Message: err.Error()})
			continue
		}

		newAbsPath := filepath.Join(filepath.Dir(oldAbsPath), newName)
		newAbsPath, err = getSafePath(cfg.OutPutPath, newAbsPath)
		if err != nil {
			results = append(results, Result{Path: path, Success: false, Message: "目标名越界"})
			continue
		}

		if _, err := os.Stat(newAbsPath); err == nil {
			results = append(results, Result{Path: path, Success: false, Message: "目标已存在"})
			continue
		}

		if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
			results = append(results, Result{Path: path, Success: false, Message: translateOSError(err)})
		} else {
			results = append(results, Result{Path: path, Success: true, Message: "成功"})
			if !info.IsDir() {
				oldBase := strings.TrimSuffix(oldAbsPath, filepath.Ext(oldAbsPath))
				newBase := strings.TrimSuffix(newAbsPath, filepath.Ext(newAbsPath))
				oldAss := oldBase + ".ass"
				newAss := newBase + ".ass"
				if _, err := os.Stat(oldAss); err == nil {
					os.Rename(oldAss, newAss)
				}
			}
		}
	}

	writeJSON(writer, commonResp{Data: results})
}

func batchDeleteFiles(writer http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(writer, commonResp{ErrNo: 400, ErrMsg: "无效请求"})
		return
	}

	cfg := configs.GetCurrentConfig()
	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}

	type Result struct {
		Path    string `json:"path"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	results := make([]Result, 0, len(body.Paths))

	for _, path := range body.Paths {
		absPath, err := getSafePath(cfg.OutPutPath, path)
		if err != nil || absPath == base {
			results = append(results, Result{Path: path, Success: false, Message: "禁止操作根目录或越权路径"})
			continue
		}

		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			assPath := strings.TrimSuffix(absPath, filepath.Ext(absPath)) + ".ass"
			if _, err := os.Stat(assPath); err == nil {
				os.Remove(assPath)
			}
		}

		if err := os.RemoveAll(absPath); err != nil {
			results = append(results, Result{Path: path, Success: false, Message: translateOSError(err)})
		} else {
			results = append(results, Result{Path: path, Success: true, Message: "成功"})
		}
	}

	writeJSON(writer, commonResp{Data: results})
}
