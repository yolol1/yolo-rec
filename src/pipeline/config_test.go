package pipeline

import (
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/stretchr/testify/assert"
)

func TestConvertLegacyConfigOrder(t *testing.T) {
	legacy := &configs.OnRecordFinished{
		SaveCover:         true,
		CustomCommandline: "echo done",
		CloudUpload: configs.CloudUpload{
			Enable:      true,
			StorageName: "115",
		},
		BiliPublish: configs.BiliPublish{
			Enable:     true,
			Tid:        21,
			PTitleTmpl: "{{ .Index }} {{ .FileName }}",
			SeasonTmpl: "{{ .HostName }} 直播录播",
			NoDynamic:  true,
		},
	}
	cfg := ConvertLegacyConfig(legacy)

	names := make([]string, 0, len(cfg.Stages))
	for _, s := range cfg.Stages {
		names = append(names, s.Name)
	}
	// 顺序：封面提取 → B站投稿 → 云上传 → 自定义命令（先投稿后上传）
	assert.Equal(t, []string{
		StageNameExtractCover,
		StageNameBiliPublish,
		StageNameCloudUpload,
		StageNameCustomCmd,
	}, names)

	// B站投稿阶段：选项正确且失败继续
	bp := cfg.Stages[1]
	assert.True(t, bp.ShouldContinueOnFailure())
	assert.Equal(t, 21, bp.GetIntOption(OptionTid, 0))
	assert.Equal(t, "{{ .Index }} {{ .FileName }}", bp.GetStringOption(OptionPTitleTmpl, ""))
	assert.Equal(t, "{{ .HostName }} 直播录播", bp.GetStringOption(OptionSeasonTmpl, ""))
	assert.True(t, bp.GetBoolOption(OptionNoDynamic, false))
	assert.Equal(t, "echo done", cfg.Stages[3].GetStringOption(OptionCommand, ""))

	// 云上传阶段：选项正确且失败继续
	cu := cfg.Stages[2]
	assert.True(t, cu.ShouldContinueOnFailure())
	assert.Equal(t, "115", cu.GetStringOption(OptionStorage, ""))
}

func TestConvertLegacyConfigCloudUploadCondition(t *testing.T) {
	// 全局 Enable=false 但配置了存储目标：仍插入阶段（房间级强制开启可生效）
	legacy := &configs.OnRecordFinished{
		CloudUpload: configs.CloudUpload{
			Enable:      false,
			StorageName: "115",
		},
	}
	cfg := ConvertLegacyConfig(legacy)
	found := false
	for _, s := range cfg.Stages {
		if s.Name == StageNameCloudUpload {
			found = true
			break
		}
	}
	assert.True(t, found)

	// 未配置存储目标：不插入阶段（即使全局 Enable=true）
	legacy2 := &configs.OnRecordFinished{
		CloudUpload: configs.CloudUpload{Enable: true},
	}
	cfg2 := ConvertLegacyConfig(legacy2)
	for _, s := range cfg2.Stages {
		assert.NotEqual(t, StageNameCloudUpload, s.Name)
	}
}

func TestConvertLegacyConfigBiliPublishDisabled(t *testing.T) {
	legacy := &configs.OnRecordFinished{
		BiliPublish: configs.BiliPublish{Enable: false},
	}
	cfg := ConvertLegacyConfig(legacy)
	for _, s := range cfg.Stages {
		assert.NotEqual(t, StageNameBiliPublish, s.Name)
	}
}
