package stages

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/openlist"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/tools"
)

// ExtractCoverStage 封面提取阶段
type ExtractCoverStage struct {
	config   pipeline.StageConfig
	commands []string
	logs     string
}

// NewExtractCoverStage 创建封面提取阶段工厂
func NewExtractCoverStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &ExtractCoverStage{
		config: config,
	}, nil
}

func (s *ExtractCoverStage) Name() string {
	return pipeline.StageNameExtractCover
}

func (s *ExtractCoverStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	var output []pipeline.FileInfo

	// 先添加所有输入文件到输出
	output = append(output, input...)

	// 对每个视频文件提取封面
	for _, file := range input {
		// 只处理视频文件
		if file.Type != pipeline.FileTypeVideo {
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		ctx.Logger.Infof("提取封面: %s", file.Path)

		// 提取封面
		coverPath, err := tools.ExtractCover(ctx.Ctx, file.Path)
		if err != nil {
			s.logs += fmt.Sprintf("提取封面失败: %s - %s\n", filepath.Base(file.Path), err.Error())
			ctx.Logger.Warnf("提取封面失败: %s - %s", file.Path, err)
			continue
		}

		// 添加封面文件到输出
		output = append(output, pipeline.FileInfo{
			Path:       coverPath,
			Type:       pipeline.FileTypeCover,
			SourcePath: file.Path,
		})

		s.logs += fmt.Sprintf("封面已保存: %s\n", filepath.Base(coverPath))
		ctx.Logger.Infof("封面已保存: %s", coverPath)
	}

	return output, nil
}

func (s *ExtractCoverStage) GetCommands() []string {
	return s.commands
}

func (s *ExtractCoverStage) GetLogs() string {
	return s.logs
}

// CloudUploadStage 云上传阶段
type CloudUploadStage struct {
	config             pipeline.StageConfig
	storageName        string
	additionalStorages []string // 额外存储目标
	pathTemplate       string
	deleteAfter        bool
	speedLimit         int      // 上传速度限制 (KB/s)，0 表示不限速
	maxConcurrent      int      // 最大并发上传数，0 或 1 表示顺序上传
	fileTypes          []string // 过滤的文件类型，空表示所有
	commands           []string
	logs               string
	mu                 sync.Mutex // 保护 logs 和 commands 的并发写入
}

// NewCloudUploadStage 创建云上传阶段工厂
func NewCloudUploadStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &CloudUploadStage{
		config:             config,
		storageName:        config.GetStringOption(pipeline.OptionStorage, ""),
		additionalStorages: config.GetStringSliceOption(pipeline.OptionAdditionalStorages),
		pathTemplate:       config.GetStringOption(pipeline.OptionPathTemplate, ""),
		deleteAfter:        config.GetBoolOption(pipeline.OptionDeleteAfter, false),
		speedLimit:         config.GetIntOption(pipeline.OptionUploadSpeedLimit, 0),
		maxConcurrent:      config.GetIntOption(pipeline.OptionMaxConcurrentUploads, 0),
		fileTypes:          config.GetStringSliceOption(pipeline.OptionFileTypes),
	}, nil
}

func (s *CloudUploadStage) Name() string {
	return pipeline.StageNameCloudUpload
}

func (s *CloudUploadStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	// 收集所有存储目标：主存储 + 额外存储
	allStorages := s.collectAllStorages()
	if len(allStorages) == 0 {
		s.logs = "未配置存储名称，跳过上传"
		ctx.Logger.Warnf("云上传: 未配置存储名称 (storage_name)，跳过")
		return input, nil
	}

	cfg := configs.GetCurrentConfig()

	// 房间级云上传开关：nil 跟随全局；true 强制开启；false 强制关闭
	// 无法匹配到房间时回退跟随全局，保持存量行为不变
	if !s.shouldUploadForRoom(ctx, cfg) {
		s.mu.Lock()
		s.logs += fmt.Sprintf("云上传: 该房间未开启云上传（房间级开关），跳过\n")
		s.mu.Unlock()
		ctx.Logger.Infof("云上传: 房间级开关已关闭（URL=%s），跳过上传", ctx.RecordInfo.LiveURL)
		return input, nil
	}

	apiUrl := cfg.OnRecordFinished.CloudUpload.ApiUrl
	username := cfg.OnRecordFinished.CloudUpload.Username
	password := cfg.OnRecordFinished.CloudUpload.Password

	if apiUrl == "" {
		s.logs = "未配置 OpenList API 地址，跳过上传"
		ctx.Logger.Warnf("云上传: 未配置 API 地址 (api_url)，跳过")
		return input, nil
	}
	if username == "" {
		s.logs = "未配置 OpenList 用户名，跳过上传"
		ctx.Logger.Warnf("云上传: 未配置用户名 (username)，跳过")
		return input, nil
	}

	ctx.Logger.Infof("云上传: 开始 (API=%s, 用户=%s, 存储=%v, 文件数=%d, 并发数=%d)", apiUrl, username, allStorages, len(input), s.effectiveConcurrency())
	s.mu.Lock()
	s.logs += fmt.Sprintf("配置: API=%s, 用户=%s, 存储=%v, 并发数=%d\n", apiUrl, username, allStorages, s.effectiveConcurrency())
	s.mu.Unlock()

	// 第1步：获取 Token
	client := openlist.NewClient(apiUrl, "")
	token, err := client.GetToken(ctx.Ctx, username, password)
	if err != nil {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 获取 Token 失败: %s\n", err.Error())
		s.mu.Unlock()
		ctx.Logger.Errorf("云上传: 获取 OpenList Token 失败: %v", err)
		return input, fmt.Errorf("云上传: 获取 OpenList Token 失败: %w", err)
	}
	client.SetToken(token)
	s.mu.Lock()
	s.logs += "✅ 登录成功，已获取 Token\n"
	s.mu.Unlock()
	ctx.Logger.Infof("云上传: 登录成功")

	// 按文件大小升序排列（小文件优先上传），已存在的文件跳过
	// 注意：排序只在开始上传前进行，已开始上传的文件不会被中断
	sortedFiles := s.sortFilesBySize(input)

	// 并发控制
	concurrency := s.effectiveConcurrency()
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	var output []pipeline.FileInfo
	var outputMu sync.Mutex
	uploadedPaths := make(map[string]string) // remotePath -> localPath
	var pathsMu sync.Mutex
	var uploadErrors []string
	var uploadErrorsMu sync.Mutex

	for _, file := range sortedFiles {
		// 文件类型过滤
		if len(s.fileTypes) > 0 && !s.matchFileType(file.Type) {
			outputMu.Lock()
			output = append(output, file)
			outputMu.Unlock()
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.mu.Lock()
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			s.mu.Unlock()
			continue
		}

		wg.Add(1)
		sem <- struct{}{} // 获取信号量

		go func(f pipeline.FileInfo) {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量

			// 渲染目标路径
			targetPath := s.renderTargetPath(ctx, f)
			if targetPath == "" {
				s.mu.Lock()
				s.logs += fmt.Sprintf("无法生成目标路径: %s\n", f.Path)
				s.mu.Unlock()
				outputMu.Lock()
				output = append(output, f)
				outputMu.Unlock()
				return
			}

			// 上传到所有存储目标
			allUploaded := true
			for _, storage := range allStorages {
				fullRemotePath := fmt.Sprintf("/%s%s", storage, targetPath)
				fullRemotePath = strings.ReplaceAll(fullRemotePath, "//", "/")

				// 检测重复路径
				pathsMu.Lock()
				if prevFile, exists := uploadedPaths[fullRemotePath]; exists {
					ctx.Logger.Warnf("云上传: 路径冲突！当前文件 %s 与之前文件 %s 的目标路径相同 (%s)，后上传的文件将覆盖前者",
						filepath.Base(f.Path), filepath.Base(prevFile), fullRemotePath)
					s.mu.Lock()
					s.logs += fmt.Sprintf("⚠️ 路径冲突: %s 和 %s 的目标路径相同 (%s)，可能覆盖\n",
						filepath.Base(f.Path), filepath.Base(prevFile), fullRemotePath)
					s.mu.Unlock()
				}
				uploadedPaths[fullRemotePath] = f.Path
				pathsMu.Unlock()

				ctx.Logger.Infof("云上传: 准备上传 %s -> %s", f.Path, fullRemotePath)
				s.mu.Lock()
				s.logs += fmt.Sprintf("上传: %s -> %s\n", filepath.Base(f.Path), fullRemotePath)
				s.commands = append(s.commands, fmt.Sprintf("upload %s to %s", f.Path, fullRemotePath))
				s.mu.Unlock()

				// 确保目标目录存在
				remoteDir := filepath.ToSlash(filepath.Dir(fullRemotePath))
				if err := client.Mkdir(ctx.Ctx, remoteDir); err != nil {
					s.mu.Lock()
					s.logs += fmt.Sprintf("⚠️ 创建目录失败 [%s]: %s（继续尝试上传）\n", remoteDir, err.Error())
					s.mu.Unlock()
					ctx.Logger.Warnf("云上传: 创建目标目录失败 [%s]: %v", remoteDir, err)
				} else {
					s.mu.Lock()
					s.logs += fmt.Sprintf("✅ 目录已就绪: %s\n", remoteDir)
					s.mu.Unlock()
				}

				// 上传文件
				uploadErr := client.Upload(ctx.Ctx, f.Path, fullRemotePath, nil, s.speedLimit)
				if uploadErr != nil {
					s.mu.Lock()
					s.logs += fmt.Sprintf("❌ 上传失败 [%s]: %s -> %s (%s)\n", storage, filepath.Base(f.Path), fullRemotePath, uploadErr.Error())
					s.mu.Unlock()
					ctx.Logger.Errorf("云上传: 上传失败 [%s] %s: %v", storage, f.Path, uploadErr)
					allUploaded = false
					uploadErrorsMu.Lock()
					uploadErrors = append(uploadErrors, fmt.Sprintf("[%s] %s -> %s (%s)", storage, filepath.Base(f.Path), fullRemotePath, uploadErr.Error()))
					uploadErrorsMu.Unlock()
				} else {
					s.mu.Lock()
					s.logs += fmt.Sprintf("✅ 上传成功 [%s]: %s -> %s\n", storage, filepath.Base(f.Path), fullRemotePath)
					s.mu.Unlock()
					ctx.Logger.Infof("云上传: 上传成功 [%s] %s", storage, fullRemotePath)
				}
			}

			// 处理本地文件：全部上传成功且配置了删除才删除
			if allUploaded && s.deleteAfter {
				if err := os.Remove(f.Path); err != nil {
					s.mu.Lock()
					s.logs += fmt.Sprintf("⚠️ 删除本地文件失败: %s (%s)\n", filepath.Base(f.Path), err.Error())
					s.mu.Unlock()
					ctx.Logger.Warnf("云上传: 删除本地文件失败 %s: %v", f.Path, err)
					outputMu.Lock()
					output = append(output, f)
					outputMu.Unlock()
				} else {
					s.mu.Lock()
					s.logs += fmt.Sprintf("🗑️ 已删除本地文件: %s\n", filepath.Base(f.Path))
					s.mu.Unlock()
				}
			} else {
				outputMu.Lock()
				output = append(output, f)
				outputMu.Unlock()
			}
		}(file)
	}

	wg.Wait()

	// 只要有上传目标失败，就让任务标记为失败，避免网页统计把失败当成成功
	if len(uploadErrors) > 0 {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 云上传: %d 个上传目标失败，任务将标记为失败，本地文件已保留\n", len(uploadErrors))
		s.mu.Unlock()
		msg := fmt.Sprintf("云上传失败: %d 个上传目标失败（本地文件已保留）", len(uploadErrors))
		const maxDetail = 5
		for i, e := range uploadErrors {
			if i >= maxDetail {
				msg += fmt.Sprintf("\n……其余 %d 个失败详情见执行日志", len(uploadErrors)-maxDetail)
				break
			}
			msg += "\n" + e
		}
		return input, errors.New(msg)
	}

	// 按原始顺序排序输出（保持文件顺序一致性）
	s.sortOutputLikeInput(output, input)

	return output, nil
}

// collectAllStorages 收集所有存储目标（主存储 + 额外存储），去重
func (s *CloudUploadStage) collectAllStorages() []string {
	seen := make(map[string]bool)
	var result []string
	if s.storageName != "" {
		seen[s.storageName] = true
		result = append(result, s.storageName)
	}
	for _, name := range s.additionalStorages {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

// matchFileType 检查文件类型是否匹配
func (s *CloudUploadStage) matchFileType(fileType pipeline.FileType) bool {
	for _, ft := range s.fileTypes {
		if strings.EqualFold(ft, string(fileType)) {
			return true
		}
	}
	return false
}

// renderTargetPath 渲染目标路径
// 使用 Go text/template 引擎，支持 {{ .HostName }}、{{ now | date "2006-01-02" }} 等模板语法
// 默认行为：模板仅定义目录结构，原始文件名自动追加到末尾
// 兼容旧模板：如果模板中包含 {{ .FileName }}，则按旧行为处理（模板定义完整路径）
func (s *CloudUploadStage) renderTargetPath(ctx *pipeline.PipelineContext, file pipeline.FileInfo) string {
	fileName := filepath.Base(file.Path)

	if s.pathTemplate == "" {
		// 默认路径：/录播归档/{平台}/{主播名}/{文件名}
		return fmt.Sprintf("/录播归档/%s/%s/%s",
			ctx.RecordInfo.Platform,
			ctx.RecordInfo.HostName,
			fileName,
		)
	}

	// 获取扩展名
	ext := filepath.Ext(file.Path)
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:]
	}

	// 获取文件修改时间
	fileModTime := ctx.RecordInfo.StartTime
	if fi, err := os.Stat(file.Path); err == nil {
		fileModTime = fi.ModTime()
	}

	data := struct {
		Platform    string
		HostName    string
		RoomName    string
		FileName    string
		Ext         string
		StartTime   time.Time
		FileModTime time.Time // 文件修改时间，可用于生成唯一文件名
	}{
		Platform:    ctx.RecordInfo.Platform,
		HostName:    ctx.RecordInfo.HostName,
		RoomName:    ctx.RecordInfo.RoomName,
		FileName:    fileName,
		Ext:         ext,
		StartTime:   ctx.RecordInfo.StartTime,
		FileModTime: fileModTime,
	}

	cfg := configs.GetCurrentConfig()
	tmpl, err := template.New("upload_path").Funcs(utils.GetFuncMap(cfg)).Parse(s.pathTemplate)
	if err != nil {
		ctx.Logger.Warnf("云上传: 路径模板解析失败: %v，使用默认路径", err)
		return fmt.Sprintf("/录播归档/%s/%s/%s",
			ctx.RecordInfo.Platform,
			ctx.RecordInfo.HostName,
			fileName,
		)
	}

	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, data); err != nil {
		ctx.Logger.Warnf("云上传: 路径模板渲染失败: %v，使用默认路径", err)
		return fmt.Sprintf("/录播归档/%s/%s/%s",
			ctx.RecordInfo.Platform,
			ctx.RecordInfo.HostName,
			fileName,
		)
	}

	renderedPath := buf.String()

	// 兼容旧模板：如果模板中包含 {{ .FileName }}，说明用户自定义了完整路径（含文件名），保持旧行为
	if strings.Contains(s.pathTemplate, "{{ .FileName }}") || strings.Contains(s.pathTemplate, "{{.FileName}}") {
		return renderedPath
	}

	// 新行为：模板仅定义目录，原始文件名追加到末尾
	if strings.HasSuffix(renderedPath, "/") {
		return renderedPath + fileName
	}
	return renderedPath + "/" + fileName
}

// effectiveConcurrency 返回有效的并发数，0 或 1 表示顺序上传
func (s *CloudUploadStage) effectiveConcurrency() int {
	if s.maxConcurrent <= 0 {
		return 1
	}
	return s.maxConcurrent
}

// sortFilesBySize 按文件大小升序排列（小文件优先），文件不存在时大小为 0
func (s *CloudUploadStage) sortFilesBySize(files []pipeline.FileInfo) []pipeline.FileInfo {
	sorted := make([]pipeline.FileInfo, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool {
		sizeI := fileSize(sorted[i].Path)
		sizeJ := fileSize(sorted[j].Path)
		return sizeI < sizeJ
	})
	return sorted
}

// shouldUploadForRoom 根据房间级云上传开关决定是否上传：
// 房间级 nil 跟随全局；true 强制开启；false 强制关闭。
// 无法匹配到房间（LiveURL 为空或房间已删除）时回退跟随全局，保持存量行为不变。
func (s *CloudUploadStage) shouldUploadForRoom(ctx *pipeline.PipelineContext, cfg *configs.Config) bool {
	globalEnabled := cfg.OnRecordFinished.CloudUpload.Enable
	if ctx.RecordInfo.LiveURL == "" {
		return globalEnabled
	}
	room, err := cfg.GetLiveRoomByUrl(ctx.RecordInfo.LiveURL)
	if err != nil {
		return globalEnabled
	}
	return room.IsCloudUploadEnabled(globalEnabled)
}

// sortOutputLikeInput 将 output 按 input 中的原始顺序排列
func (s *CloudUploadStage) sortOutputLikeInput(output, input []pipeline.FileInfo) {
	// 构建 input 中的顺序索引
	order := make(map[string]int, len(input))
	for i, f := range input {
		order[f.Path] = i
	}
	sort.Slice(output, func(i, j int) bool {
		oi, okI := order[output[i].Path]
		oj, okJ := order[output[j].Path]
		if !okI && !okJ {
			return false
		}
		if !okI {
			return false
		}
		if !okJ {
			return true
		}
		return oi < oj
	})
}

// fileSize 获取文件大小，文件不存在时返回 0
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (s *CloudUploadStage) GetCommands() []string {
	return s.commands
}

func (s *CloudUploadStage) GetLogs() string {
	return s.logs
}
