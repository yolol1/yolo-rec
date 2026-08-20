package stages

import (
	"bytes"
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
	"github.com/bililive-go/bililive-go/src/pkg/bilipublish"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

// BiliPublishStage B站投稿阶段
// 投稿在云上传之前执行，确保云上传删除本地文件前投稿已完成
type BiliPublishStage struct {
	config            pipeline.StageConfig
	titleTemplate     string   // 标题模板
	pTitleTemplate    string   // 分P标题模板（多文件投稿时每个分P的标题，留空使用主标题）
	descTemplate      string   // 简介模板
	tid               int      // B站分区 tid
	tags              []string // 标签
	dtime             string   // 定时发布时间（RFC3339，留空立即发布）
	coverUseExtracted bool     // 是否使用提取的封面
	deleteAfter       bool     // 投稿成功后删除本地视频
	commands          []string
	logs              string
	mu                sync.Mutex // 保护 logs 和 commands 的并发写入
}

// NewBiliPublishStage 创建 B站投稿阶段工厂
func NewBiliPublishStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	return &BiliPublishStage{
		config:            config,
		titleTemplate:     config.GetStringOption(pipeline.OptionTitleTmpl, ""),
		pTitleTemplate:    config.GetStringOption(pipeline.OptionPTitleTmpl, ""),
		descTemplate:      config.GetStringOption(pipeline.OptionDescTmpl, ""),
		tid:               config.GetIntOption(pipeline.OptionTid, 0),
		tags:              config.GetStringSliceOption(pipeline.OptionTags),
		dtime:             config.GetStringOption(pipeline.OptionDTime, ""),
		coverUseExtracted: config.GetBoolOption(pipeline.OptionCoverExtracted, true),
		deleteAfter:       config.GetBoolOption(pipeline.OptionDeleteAfter, false),
	}, nil
}

func (s *BiliPublishStage) Name() string {
	return pipeline.StageNameBiliPublish
}

func (s *BiliPublishStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
		return input, nil
	}

	// 房间级开关：默认不投稿，只有用户单独开启的房间才会投稿
	if !s.shouldPublishForRoom(ctx) {
		s.mu.Lock()
		s.logs += "B站投稿: 该房间未开启投稿（房间级开关未开启），跳过\n"
		s.mu.Unlock()
		ctx.Logger.Infof("B站投稿: 房间未开启投稿（URL=%s），跳过", ctx.RecordInfo.LiveURL)
		return input, nil
	}

	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		s.mu.Lock()
		s.logs += "B站投稿: 全局配置不可用，跳过\n"
		s.mu.Unlock()
		ctx.Logger.Infof("B站投稿: 全局配置不可用，跳过")
		return input, nil
	}

	// 收集所有视频文件（多文件将作为同一稿件的多个分P），按文件名排序保持分段顺序
	videoFiles := collectVideoFiles(input)
	if len(videoFiles) == 0 {
		s.logs = "B站投稿: 没有找到视频文件，跳过"
		ctx.Logger.Warnf("B站投稿: 没有找到视频文件，跳过")
		return input, nil
	}

	// 过滤已不存在的视频文件（如前置阶段已删除源文件），全部不存在则跳过
	existingVideos := make([]pipeline.FileInfo, 0, len(videoFiles))
	for _, vf := range videoFiles {
		if _, err := os.Stat(vf.Path); os.IsNotExist(err) {
			s.mu.Lock()
			s.logs += fmt.Sprintf("B站投稿: 视频文件不存在，跳过该分P: %s\n", vf.Path)
			s.mu.Unlock()
			ctx.Logger.Warnf("B站投稿: 视频文件不存在，跳过该分P: %s", vf.Path)
			continue
		}
		existingVideos = append(existingVideos, vf)
	}
	if len(existingVideos) == 0 {
		s.logs = "B站投稿: 所有视频文件都不存在，跳过"
		ctx.Logger.Warnf("B站投稿: 所有视频文件都不存在，跳过")
		return input, nil
	}

	// 渲染标题和简介模板
	title, err := s.renderTemplate(ctx, s.titleTemplate, existingVideos[0], 0)
	if err != nil {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 标题模板渲染失败: %s\n", err.Error())
		s.mu.Unlock()
		return input, fmt.Errorf("B站投稿: 标题模板渲染失败: %w", err)
	}
	desc, err := s.renderTemplate(ctx, s.descTemplate, existingVideos[0], 0)
	if err != nil {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 简介模板渲染失败: %s\n", err.Error())
		s.mu.Unlock()
		return input, fmt.Errorf("B站投稿: 简介模板渲染失败: %w", err)
	}

	// 解析定时发布时间（RFC3339，留空立即发布）
	var publishTime time.Time
	if strings.TrimSpace(s.dtime) != "" {
		publishTime, err = time.Parse(time.RFC3339, strings.TrimSpace(s.dtime))
		if err != nil {
			s.mu.Lock()
			s.logs += fmt.Sprintf("❌ 定时发布时间格式无效（应为 RFC3339，如 2026-08-20T10:00:00+08:00）: %s\n", s.dtime)
			s.mu.Unlock()
			return input, fmt.Errorf("B站投稿: 定时发布时间格式无效: %w", err)
		}
	}

	// 组装每个分P
	req := bilipublish.PublishRequest{
		Title: title,
		Desc:  desc,
		Tid:   s.tid,
		Tags:  s.tags,
		DTime: publishTime,
	}
	for i, vf := range existingVideos {
		pTitle := title
		if strings.TrimSpace(s.pTitleTemplate) != "" {
			pTitle, err = s.renderTemplate(ctx, s.pTitleTemplate, vf, i+1)
			if err != nil {
				s.mu.Lock()
				s.logs += fmt.Sprintf("❌ 分P %d 标题模板渲染失败: %s\n", i+1, err.Error())
				s.mu.Unlock()
				return input, fmt.Errorf("B站投稿: 分P %d 标题模板渲染失败: %w", i+1, err)
			}
		}
		req.Files = append(req.Files, bilipublish.PublishFile{
			FilePath: vf.Path,
			Title:    pTitle,
			Desc:     desc,
		})
	}

	// 封面：仅当配置启用且存在提取的封面文件时才上传。
	// 优先取与第一个分P关联的封面，找不到时回退到第一个封面文件
	if s.coverUseExtracted {
		if cover := findCoverForVideo(input, existingVideos[0].Path); cover != nil {
			if _, err := os.Stat(cover.Path); err == nil {
				req.CoverPath = cover.Path
			}
		}
	}

	// 解析 Cookie 并创建投稿客户端
	cookie := bilipublish.ResolveCookie(cfg.OnRecordFinished.BiliPublish, cfg.Cookies)
	client := bilipublish.NewClient(cookie)

	s.mu.Lock()
	s.logs += fmt.Sprintf("开始投稿: 共 %d 个分P\n标题: %s\n", len(req.Files), title)
	for i, f := range req.Files {
		s.logs += fmt.Sprintf("分P %d/%d: %s\n", i+1, len(req.Files), filepath.Base(f.FilePath))
	}
	s.commands = append(s.commands, fmt.Sprintf("publish %d file(s) to bilibili", len(req.Files)))
	s.mu.Unlock()
	ctx.Logger.Infof("B站投稿: 开始投稿 %d 个分P（标题: %s）", len(req.Files), title)

	result, err := client.Publish(ctx.Ctx, req)
	if err != nil {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 投稿失败: %s\n", err.Error())
		s.mu.Unlock()
		ctx.Logger.Errorf("B站投稿: 投稿失败: %v", err)
		// 返回错误但不删除文件；executor 的 continue_on_failure 保证不阻断后续云上传
		return input, fmt.Errorf("B站投稿失败: %w", err)
	}

	s.mu.Lock()
	s.logs += fmt.Sprintf("✅ 投稿成功: BV%s (av%d)，共 %d 个分P\n", result.BVID, result.AID, len(req.Files))
	s.mu.Unlock()
	ctx.Logger.Infof("B站投稿: 投稿成功 BV%s (av%d)，共 %d 个分P", result.BVID, result.AID, len(req.Files))

	// 投稿成功后按配置删除本地视频文件（逐个删除，删除失败只记录日志不阻断）
	if s.deleteAfter {
		for _, f := range req.Files {
			if err := os.Remove(f.FilePath); err != nil {
				s.mu.Lock()
				s.logs += fmt.Sprintf("⚠️ 删除本地视频失败: %s (%s)\n", filepath.Base(f.FilePath), err.Error())
				s.mu.Unlock()
				ctx.Logger.Warnf("B站投稿: 删除本地视频失败 %s: %v", f.FilePath, err)
			} else {
				s.mu.Lock()
				s.logs += fmt.Sprintf("🗑️ 已删除本地视频: %s\n", filepath.Base(f.FilePath))
				s.mu.Unlock()
			}
		}
	}

	// 文件继续流转给后续阶段（如云上传）
	return input, nil
}

// shouldPublishForRoom 判断当前录制是否应该投稿：
// 能匹配到房间，且房间级 BiliPublish 开关为 true（默认不投稿）。
// 投稿目标为 B站，与录制来源平台无关，任意平台房间均可开启投稿。
func (s *BiliPublishStage) shouldPublishForRoom(ctx *pipeline.PipelineContext) bool {
	if ctx.RecordInfo.LiveURL == "" {
		return false
	}
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return false
	}
	room, err := cfg.GetLiveRoomByUrl(ctx.RecordInfo.LiveURL)
	if err != nil {
		return false
	}
	return room.IsBiliPublish()
}

// collectVideoFiles 收集输入中的视频文件并按文件名排序（保持分段顺序）
func collectVideoFiles(input []pipeline.FileInfo) []pipeline.FileInfo {
	var videos []pipeline.FileInfo
	for _, f := range input {
		if f.Type == pipeline.FileTypeVideo {
			videos = append(videos, f)
		}
	}
	sort.SliceStable(videos, func(i, j int) bool {
		return filepath.Base(videos[i].Path) < filepath.Base(videos[j].Path)
	})
	return videos
}

// findCoverForVideo 查找与指定视频关联的封面文件（extract_cover 生成的封面 SourcePath 指向源视频）
func findCoverForVideo(input []pipeline.FileInfo, videoPath string) *pipeline.FileInfo {
	for i := range input {
		if input[i].Type == pipeline.FileTypeCover && input[i].SourcePath == videoPath {
			return &input[i]
		}
	}
	// 回退：取第一个封面文件
	for i := range input {
		if input[i].Type == pipeline.FileTypeCover {
			return &input[i]
		}
	}
	return nil
}

// renderTemplate 渲染投稿模板（标题/简介/分P标题）
// 支持 {{ .Platform }}、{{ .HostName }}、{{ .RoomName }}、{{ .FileName }}、{{ .Ext }}、{{ .StartTime }}、{{ .Index }} 等
// index 为分P序号（从 1 开始），主标题/简介传 0
func (s *BiliPublishStage) renderTemplate(ctx *pipeline.PipelineContext, tmplText string, file pipeline.FileInfo, index int) (string, error) {
	if strings.TrimSpace(tmplText) == "" {
		return "", nil
	}

	ext := filepath.Ext(file.Path)
	if len(ext) > 0 && ext[0] == '.' {
		ext = ext[1:]
	}
	data := struct {
		Platform  string
		HostName  string
		RoomName  string
		FileName  string
		Ext       string
		StartTime time.Time
		Index     int
	}{
		Platform:  ctx.RecordInfo.Platform,
		HostName:  ctx.RecordInfo.HostName,
		RoomName:  ctx.RecordInfo.RoomName,
		FileName:  filepath.Base(file.Path),
		Ext:       ext,
		StartTime: ctx.RecordInfo.StartTime,
		Index:     index,
	}

	cfg := configs.GetCurrentConfig()
	tmpl, err := template.New("bili_publish").Funcs(utils.GetFuncMap(cfg)).Parse(tmplText)
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}

func (s *BiliPublishStage) GetCommands() []string {
	return s.commands
}

func (s *BiliPublishStage) GetLogs() string {
	return s.logs
}
