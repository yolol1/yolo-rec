package servers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/configs"
)

// getSafePath 校验子路径不会逃逸出根目录，返回安全的绝对路径
func getSafePath(base, subPath string) (string, error) {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(filepath.Join(absBase, subPath))
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", err
	}

	// 核心安全逻辑：如果计算出的相对路径以 ".." 开头，说明它逃逸到了 base 目录之外
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("非法路径访问：超出授权范围")
	}

	return absTarget, nil
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

	base, err := filepath.Abs(cfg.OutPutPath)
	if err != nil {
		writeJSON(writer, commonResp{ErrNo: 500, ErrMsg: "获取根目录绝对路径失败: " + err.Error()})
		return
	}
	rel, err := filepath.Rel(base, newAbsPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
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

		newAbsPath := filepath.Join(filepath.Dir(oldAbsPath), newName)
		rel, err := filepath.Rel(base, newAbsPath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
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
