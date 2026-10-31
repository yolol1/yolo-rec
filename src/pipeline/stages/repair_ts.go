package stages

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const streamCopyMode = "copy"

// RepairTsStage TS 时间戳与索引修复阶段。
// 通过流复制重建时间戳和 MPEG-TS 周期表，不重新编码音视频。
type RepairTsStage struct {
	commands []string
	logs     string
}

// NewRepairTsStage 创建 TS 修复阶段。
func NewRepairTsStage(_ pipeline.StageConfig) (pipeline.Stage, error) {
	return &RepairTsStage{}, nil
}

func (s *RepairTsStage) Name() string {
	return pipeline.StageNameRepairTs
}

func (s *RepairTsStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有可修复的 TS 输入文件"
		return input, nil
	}

	ffmpegPath := ctx.FFmpegPath
	if ffmpegPath == "" {
		var err error
		ffmpegPath, err = utils.GetFFmpegPath(ctx.Ctx)
		if err != nil {
			s.logs = fmt.Sprintf("ffmpeg 不可用: %s", err.Error())
			return nil, fmt.Errorf("ffmpeg not available: %w", err)
		}
	}

	output := make([]pipeline.FileInfo, 0, len(input))
	for _, file := range input {
		if file.Type != pipeline.FileTypeVideo || !strings.EqualFold(filepath.Ext(file.Path), ".ts") {
			output = append(output, file)
			continue
		}

		if err := s.repairFile(ctx, ffmpegPath, file.Path); err != nil {
			return nil, err
		}
		output = append(output, file)
	}

	return output, nil
}

func (s *RepairTsStage) repairFile(ctx *pipeline.PipelineContext, ffmpegPath, inputPath string) error {
	info, err := os.Stat(inputPath)
	if err != nil {
		s.logs += fmt.Sprintf("文件不存在或无法读取: %s (%v)\n", inputPath, err)
		return fmt.Errorf("stat TS file %s: %w", inputPath, err)
	}
	if info.Size() == 0 {
		s.logs += fmt.Sprintf("TS 文件为空，跳过修复: %s\n", inputPath)
		return nil
	}

	inputStreams := inspectMediaStreams(ctx.Ctx, ffmpegPath, inputPath)
	if !inputStreams.video {
		return fmt.Errorf("TS file has no video stream: %s", inputPath)
	}
	if !inputStreams.audio {
		ctx.Logger.Warnf("源 TS 本身没有音频轨道，无法通过重封装恢复声音: %s", inputPath)
	}

	tempPath := filepath.Join(
		filepath.Dir(inputPath),
		".repairing_"+filepath.Base(inputPath),
	)
	defer os.Remove(tempPath)

	args := buildRepairTsArgs(inputPath, tempPath)
	command := fmt.Sprintf("%s %s", ffmpegPath, strings.Join(args, " "))
	s.commands = append(s.commands, command)

	ctx.Logger.Infof("开始修复 TS 时间轴: %s", inputPath)
	cmd := exec.CommandContext(ctx.Ctx, ffmpegPath, args...)
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		detail := truncateCommandOutput(string(cmdOutput), 4096)
		s.logs += fmt.Sprintf("TS 修复失败: %s\n%s\n", inputPath, detail)
		return fmt.Errorf("repair TS %s: %w", inputPath, err)
	}

	tempInfo, err := os.Stat(tempPath)
	if err != nil {
		return fmt.Errorf("stat repaired TS file %s: %w", tempPath, err)
	}
	if tempInfo.Size() == 0 {
		return fmt.Errorf("repaired TS file is empty: %s", tempPath)
	}

	outputStreams := inspectMediaStreams(ctx.Ctx, ffmpegPath, tempPath)
	if !outputStreams.video {
		return fmt.Errorf("repaired TS file lost its video stream: %s", tempPath)
	}
	if inputStreams.audio && !outputStreams.audio {
		return fmt.Errorf("repaired TS file lost its audio stream: %s", tempPath)
	}

	leftoverBackup, err := replaceFileSafely(inputPath, tempPath)
	if err != nil {
		s.logs += fmt.Sprintf("替换 TS 文件失败: %s (%v)\n", inputPath, err)
		return err
	}
	if leftoverBackup != "" {
		s.logs += fmt.Sprintf("TS 已修复，但旧文件备份未能删除: %s\n", leftoverBackup)
		ctx.Logger.Warnf("TS 已修复，但旧文件备份未能删除: %s", leftoverBackup)
	}

	s.logs += fmt.Sprintf("修复完成: %s\n", filepath.Base(inputPath))
	ctx.Logger.Infof("TS 时间轴修复完成: %s", inputPath)
	return nil
}

func buildRepairTsArgs(inputPath, outputPath string) []string {
	return []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostdin",
		"-y",
		"-fflags", "+genpts+igndts+discardcorrupt",
		"-i", inputPath,
		"-map", "0:v:0?",
		"-map", "0:a?",
		"-c", streamCopyMode,
		"-max_muxing_queue_size", "9999",
		"-avoid_negative_ts", "make_zero",
		"-mpegts_flags", "+resend_headers+initial_discontinuity",
		"-f", "mpegts",
		outputPath,
	}
}

type mediaStreams struct {
	video bool
	audio bool
}

func inspectMediaStreams(ctx context.Context, ffmpegPath, mediaPath string) mediaStreams {
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-nostdin", "-i", mediaPath)
	output, _ := cmd.CombinedOutput()
	return parseMediaStreams(string(output))
}

func parseMediaStreams(output string) mediaStreams {
	return mediaStreams{
		video: strings.Contains(output, "Video:"),
		audio: strings.Contains(output, "Audio:"),
	}
}

// replaceFileSafely 在目标同目录内完成替换。替换完成后才删除备份；
// 任一步失败都会尽量恢复原文件，避免修复失败导致录像丢失。
func replaceFileSafely(originalPath, replacementPath string) (string, error) {
	backupPath := fmt.Sprintf("%s.repair-backup-%d", originalPath, time.Now().UnixNano())
	if err := os.Rename(originalPath, backupPath); err != nil {
		return "", fmt.Errorf("backup original TS file: %w", err)
	}

	if err := os.Rename(replacementPath, originalPath); err != nil {
		if restoreErr := os.Rename(backupPath, originalPath); restoreErr != nil {
			return backupPath, fmt.Errorf("replace TS file: %w (restore failed: %v, backup: %s)", err, restoreErr, backupPath)
		}
		return "", fmt.Errorf("replace TS file: %w", err)
	}

	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return backupPath, nil
	}
	return "", nil
}

func truncateCommandOutput(output string, maxLen int) string {
	output = strings.TrimSpace(output)
	if maxLen <= 0 || len(output) <= maxLen {
		return output
	}
	return output[len(output)-maxLen:]
}

func (s *RepairTsStage) GetCommands() []string {
	return s.commands
}

func (s *RepairTsStage) GetLogs() string {
	return s.logs
}
