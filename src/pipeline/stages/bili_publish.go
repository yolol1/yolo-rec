package stages

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
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
		s.logs += "B站投稿: 该房间未开启投稿（平台不是 bilibili 或房间级开关未开启），跳过\n"
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

	// 从输入中查找视频文件和封面文件
	var videoFile *pipeline.FileInfo
	var coverFile *pipeline.FileInfo
	for i := range input {
		switch input[i].Type {
		case pipeline.FileTypeVideo:
			if videoFile == nil {
				f := input[i]
				videoFile = &f
			}
		case pipeline.FileTypeCover:
			if coverFile == nil {
				f := input[i]
				coverFile = &f
			}
		}
	}
	if videoFile == nil {
		s.logs = "B站投稿: 没有找到视频文件，跳过"
		ctx.Logger.Warnf("B站投稿: 没有找到视频文件，跳过")
		return input, nil
	}
	if _, err := os.Stat(videoFile.Path); os.IsNotExist(err) {
		s.mu.Lock()
		s.logs += fmt.Sprintf("B站投稿: 视频文件不存在: %s\n", videoFile.Path)
		s.mu.Unlock()
		ctx.Logger.Warnf("B站投稿: 视频文件不存在: %s", videoFile.Path)
		return input, nil
	}

	// 渲染标题和简介模板
	title, err := s.renderTemplate(ctx, s.titleTemplate, *videoFile)
	if err != nil {
		s.mu.Lock()
		s.logs += fmt.Sprintf("❌ 标题模板渲染失败: %s\n", err.Error())
		s.mu.Unlock()
		return input, fmt.Errorf("B站投稿: 标题模板渲染失败: %w", err)
	}
	desc, err := s.renderTemplate(ctx, s.descTemplate, *videoFile)
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

	req := bilipublish.PublishRequest{
		FilePath: videoFile.Path,
		Title:    title,
		Desc:     desc,
		Tid:      s.tid,
		Tags:     s.tags,
		DTime:    publishTime,
	}

	// 封面：仅当配置启用且存在提取的封面文件时才上传
	if s.coverUseExtracted && coverFile != nil {
		if _, err := os.Stat(coverFile.Path); err == nil {
			req.CoverPath = coverFile.Path
		}
	}

	// 解析 Cookie 并创建投稿客户端
	cookie := bilipublish.ResolveCookie(cfg.OnRecordFinished.BiliPublish, cfg.Cookies)
	client := bilipublish.NewClient(cookie)

	s.mu.Lock()
	s.logs += fmt.Sprintf("开始投稿: %s\n标题: %s\n", filepath.Base(videoFile.Path), title)
	s.commands = append(s.commands, fmt.Sprintf("publish %s to bilibili", videoFile.Path))
	s.mu.Unlock()
	ctx.Logger.Infof("B站投稿: 开始投稿 %s（标题: %s）", videoFile.Path, title)

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
	s.logs += fmt.Sprintf("✅ 投稿成功: BV%s (av%d)\n", result.BVID, result.AID)
	s.mu.Unlock()
	ctx.Logger.Infof("B站投稿: 投稿成功 BV%s (av%d)", result.BVID, result.AID)

	// 投稿成功后按配置删除本地视频文件
	if s.deleteAfter {
		if err := os.Remove(videoFile.Path); err != nil {
			s.mu.Lock()
			s.logs += fmt.Sprintf("⚠️ 删除本地视频失败: %s (%s)\n", filepath.Base(videoFile.Path), err.Error())
			s.mu.Unlock()
			ctx.Logger.Warnf("B站投稿: 删除本地视频失败 %s: %v", videoFile.Path, err)
		} else {
			s.mu.Lock()
			s.logs += fmt.Sprintf("🗑️ 已删除本地视频: %s\n", filepath.Base(videoFile.Path))
			s.mu.Unlock()
		}
	}

	// 文件继续流转给后续阶段（如云上传）
	return input, nil
}

// shouldPublishForRoom 判断当前录制是否应该投稿：
// 平台必须为 bilibili，且能匹配到房间，且房间级 BiliPublish 开关为 true（默认不投稿）
func (s *BiliPublishStage) shouldPublishForRoom(ctx *pipeline.PipelineContext) bool {
	platform := strings.TrimSpace(ctx.RecordInfo.Platform)
	if platform != "哔哩哔哩" && !strings.EqualFold(platform, "bilibili") {
		return false
	}
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

// renderTemplate 渲染投稿模板（标题/简介）
// 支持 {{ .Platform }}、{{ .HostName }}、{{ .RoomName }}、{{ .FileName }}、{{ .Ext }}、{{ .StartTime }} 等
func (s *BiliPublishStage) renderTemplate(ctx *pipeline.PipelineContext, tmplText string, file pipeline.FileInfo) (string, error) {
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
	}{
		Platform:  ctx.RecordInfo.Platform,
		HostName:  ctx.RecordInfo.HostName,
		RoomName:  ctx.RecordInfo.RoomName,
		FileName:  filepath.Base(file.Path),
		Ext:       ext,
		StartTime: ctx.RecordInfo.StartTime,
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
