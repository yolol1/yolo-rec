package stages

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/stretchr/testify/assert"
)

func newBiliPublishStage() pipeline.Stage {
	s, err := NewBiliPublishStage(pipeline.StageConfig{})
	if err != nil {
		panic(err)
	}
	return s
}

func newPipelineContext(platform, liveURL string) *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Ctx:    context.Background(),
		Logger: livelogger.New(0, nil),
		RecordInfo: pipeline.RecordInfo{
			Platform: platform,
			LiveURL:  liveURL,
		},
	}
}

func TestBiliPublishStageNonBilibiliPlatform(t *testing.T) {
	// 投稿目标是 B站，与录制平台无关：非 B站平台房间开启投稿后同样进入投稿流程
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.Enable = true
	cfg.OnRecordFinished.BiliPublish.TitleTmpl = "标题"
	cfg.OnRecordFinished.BiliPublish.Tid = 21
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://www.douyu.com/123", BiliPublish: configs.BoolPtr(true)},
	}
	configs.SetCurrentConfig(cfg)

	// 需要一个真实存在的视频文件才能走到投稿客户端
	videoPath := filepath.Join(t.TempDir(), "a.flv")
	assert.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0o644))

	stage := newBiliPublishStage()
	ctx := newPipelineContext("斗鱼", "https://www.douyu.com/123")
	input := []pipeline.FileInfo{{Path: videoPath, Type: pipeline.FileTypeVideo}}
	_, err := stage.Execute(ctx, input)
	// 房间开关已开启 → 进入投稿流程；未配置 Cookie → 投稿失败（而不是跳过）
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "B站投稿失败")
}

func TestBiliPublishStageNonBilibiliPlatformSkipWhenNotEnabled(t *testing.T) {
	// 非 B站平台房间同样遵守安全默认：房间级开关未开启则不投稿
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.Enable = true
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://www.douyu.com/123"}, // BiliPublish=nil 默认不投稿
	}
	configs.SetCurrentConfig(cfg)
	stage := newBiliPublishStage()
	ctx := newPipelineContext("斗鱼", "https://www.douyu.com/123")
	input := []pipeline.FileInfo{{Path: "a.flv", Type: pipeline.FileTypeVideo}}
	output, err := stage.Execute(ctx, input)
	assert.NoError(t, err)
	assert.Equal(t, input, output)
}

func TestBiliPublishStageSkipEmptyLiveURL(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.Enable = true
	configs.SetCurrentConfig(cfg)
	stage := newBiliPublishStage()
	ctx := newPipelineContext("哔哩哔哩", "")
	input := []pipeline.FileInfo{{Path: "a.flv", Type: pipeline.FileTypeVideo}}
	output, err := stage.Execute(ctx, input)
	assert.NoError(t, err)
	assert.Equal(t, input, output)
}

func TestBiliPublishStageSkipRoomNotEnabled(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.Enable = true
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/123"}, // BiliPublish=nil 默认不投稿
	}
	configs.SetCurrentConfig(cfg)
	stage := newBiliPublishStage()
	ctx := newPipelineContext("哔哩哔哩", "https://live.bilibili.com/123")
	input := []pipeline.FileInfo{{Path: "a.flv", Type: pipeline.FileTypeVideo}}
	output, err := stage.Execute(ctx, input)
	assert.NoError(t, err)
	assert.Equal(t, input, output)
}

func TestBiliPublishStageFailWithoutCookie(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.TitleTmpl = "标题"
	cfg.OnRecordFinished.BiliPublish.Tid = 21
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/123", BiliPublish: configs.BoolPtr(true)},
	}
	configs.SetCurrentConfig(cfg)

	// 需要一个真实存在的视频文件才能走到投稿客户端
	videoPath := filepath.Join(t.TempDir(), "a.flv")
	assert.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0o644))

	stage := newBiliPublishStage()
	ctx := newPipelineContext("哔哩哔哩", "https://live.bilibili.com/123")
	input := []pipeline.FileInfo{{Path: videoPath, Type: pipeline.FileTypeVideo}}
	_, err := stage.Execute(ctx, input)
	// 房间开关已开启且阶段已被插入（迁移时按构建配置判断），执行时不再检查全局开关；
	// 未配置 Cookie → 投稿失败（而不是跳过）
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "B站投稿失败")
}

func TestCollectVideoFilesSortsByName(t *testing.T) {
	input := []pipeline.FileInfo{
		{Path: filepath.Join("rec", "b.flv"), Type: pipeline.FileTypeVideo},
		{Path: filepath.Join("rec", "a.flv"), Type: pipeline.FileTypeVideo},
		{Path: filepath.Join("rec", "cover.jpg"), Type: pipeline.FileTypeCover},
	}
	videos := collectVideoFiles(input)
	assert.Len(t, videos, 2)
	assert.Equal(t, "a.flv", filepath.Base(videos[0].Path))
	assert.Equal(t, "b.flv", filepath.Base(videos[1].Path))
}

func TestFindCoverForVideo(t *testing.T) {
	input := []pipeline.FileInfo{
		{Path: "a.flv", Type: pipeline.FileTypeVideo},
		{Path: "a.jpg", Type: pipeline.FileTypeCover, SourcePath: "a.flv"},
		{Path: "b.jpg", Type: pipeline.FileTypeCover, SourcePath: "b.flv"},
	}
	cover := findCoverForVideo(input, "a.flv")
	assert.NotNil(t, cover)
	assert.Equal(t, "a.jpg", cover.Path)

	// 找不到关联封面时回退到第一个封面
	cover = findCoverForVideo(input, "missing.flv")
	assert.NotNil(t, cover)
	assert.Equal(t, "a.jpg", cover.Path)
}
