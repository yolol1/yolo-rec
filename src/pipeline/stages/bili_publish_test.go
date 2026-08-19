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

func TestBiliPublishStageSkipNonBilibili(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OnRecordFinished.BiliPublish.Enable = true
	cfg.LiveRooms = []configs.LiveRoom{
		{Url: "https://live.bilibili.com/123", BiliPublish: configs.BoolPtr(true)},
	}
	configs.SetCurrentConfig(cfg)
	stage := newBiliPublishStage()
	ctx := newPipelineContext("斗鱼", "https://live.bilibili.com/123")
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
	cfg.OnRecordFinished.BiliPublish.Enable = true
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
	// 房间和全局开关都已开启，但未配置 Cookie → 投稿失败（而不是跳过）
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "B站投稿失败")
}
