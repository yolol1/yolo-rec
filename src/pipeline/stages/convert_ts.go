package stages

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bililive-go/bililive-go/src/pipeline"
	bilisentry "github.com/bililive-go/bililive-go/src/pkg/sentry"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/sirupsen/logrus"
)

// ConvertTsStage TS 转换阶段 (Remuxing)
type ConvertTsStage struct {
	config       pipeline.StageConfig
	deleteSource bool
	commands     []string
	logs         string
}

// NewConvertTsStage 创建 TS 转换阶段工厂
func NewConvertTsStage(config pipeline.StageConfig) (pipeline.Stage, error) {
	deleteSource := config.GetBoolOption(pipeline.OptionDeleteSource, false)
	return &ConvertTsStage{
		config:       config,
		deleteSource: deleteSource,
	}, nil
}

func (s *ConvertTsStage) Name() string {
	return pipeline.StageNameConvertTs
}

func (s *ConvertTsStage) Execute(ctx *pipeline.PipelineContext, input []pipeline.FileInfo) ([]pipeline.FileInfo, error) {
	if len(input) == 0 {
		s.logs = "没有输入文件"
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

	var output []pipeline.FileInfo

	for _, file := range input {
		// 只处理视频文件
		if file.Type != pipeline.FileTypeVideo {
			output = append(output, file)
			continue
		}

		// 检查文件是否存在
		if _, err := os.Stat(file.Path); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("文件不存在: %s\n", file.Path)
			continue
		}

		// 如果已经是 TS 文件，跳过
		ext := strings.ToLower(filepath.Ext(file.Path))
		if ext == ".ts" {
			s.logs += fmt.Sprintf("文件 %s 已经是 TS 格式，跳过转换。\n", filepath.Base(file.Path))
			output = append(output, file)
			continue
		}

		// 确定输出文件名
		base := strings.TrimSuffix(file.Path, ext)
		outputPath := base + ".ts"

		// 临时文件路径
		dir := filepath.Dir(outputPath)
		baseName := filepath.Base(outputPath)
		tempFile := filepath.Join(dir, ".converting_"+baseName)

		ctx.Logger.Infof("转封装 TS: %s -> %s", file.Path, outputPath)

		// 获取视频时长用于进度计算
		duration := s.getVideoDuration(ctx.Ctx, ffmpegPath, file.Path)
		// 探测视频流的 Codec，用于选择对应的比特流过滤器
		codec := s.getVideoCodec(ctx.Ctx, ffmpegPath, file.Path)

		// 构建 ffmpeg 命令
		args := []string{
			"-i", file.Path,
			"-c", "copy",
		}

		// 根据 Codec 选用合适的比特流过滤器
		if codec != "" {
			if strings.Contains(codec, "hevc") || strings.Contains(codec, "h265") {
				args = append(args, "-bsf:v", "hevc_mp4toannexb")
			} else if strings.Contains(codec, "h264") || strings.Contains(codec, "avc") {
				args = append(args, "-bsf:v", "h264_mp4toannexb")
			}
		} else {
			// 缺省情况尝试用 h264_mp4toannexb
			args = append(args, "-bsf:v", "h264_mp4toannexb")
		}

		args = append(args,
			"-f", "mpegts",
			"-y",
			"-progress", "pipe:1",
			tempFile,
		)

		cmdStr := fmt.Sprintf("%s %s", ffmpegPath, strings.Join(args, " "))
		s.commands = append(s.commands, cmdStr)

		cmd := exec.CommandContext(ctx.Ctx, ffmpegPath, args...)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			s.logs += fmt.Sprintf("创建输出管道失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			s.logs += fmt.Sprintf("启动 ffmpeg 失败: %s\n", err.Error())
			return nil, fmt.Errorf("failed to start ffmpeg: %w", err)
		}

		// 解析进度（后台）
		bilisentry.GoWithContext(ctx.Ctx, func(goCtx context.Context) {
			s.parseProgress(goCtx, stdout, duration)
		})

		// 等待命令完成
		if err := cmd.Wait(); err != nil {
			os.Remove(tempFile)
			s.logs += fmt.Sprintf("ffmpeg 转换失败: %s - %s\n", file.Path, err.Error())
			return nil, fmt.Errorf("ffmpeg conversion failed for %s: %w", file.Path, err)
		}

		// 检查临时文件
		if _, err := os.Stat(tempFile); os.IsNotExist(err) {
			s.logs += fmt.Sprintf("临时文件未创建: %s\n", tempFile)
			return nil, fmt.Errorf("temp file was not created: %s", tempFile)
		}

		// 重命名临时文件
		if err := os.Rename(tempFile, outputPath); err != nil {
			os.Remove(tempFile)
			return nil, fmt.Errorf("failed to rename temp file: %w", err)
		}

		// 添加输出文件
		output = append(output, pipeline.FileInfo{
			Path:       outputPath,
			Type:       pipeline.FileTypeVideo,
			SourcePath: file.Path,
		})

		// 删除原始文件
		if s.deleteSource && file.Path != outputPath {
			if err := os.Remove(file.Path); err != nil {
				logrus.WithError(err).WithField("file", file.Path).Warn("failed to delete original file")
				s.logs += fmt.Sprintf("删除原始文件失败: %s\n", file.Path)
			} else {
				s.logs += fmt.Sprintf("已删除原始文件: %s\n", file.Path)
				ctx.Logger.Infof("已删除原始文件: %s", file.Path)
			}
		} else {
			// 保留原始文件在输出中
			if !s.deleteSource {
				output = append(output, file)
			}
		}

		s.logs += fmt.Sprintf("转换完成: %s -> %s\n", filepath.Base(file.Path), filepath.Base(outputPath))
		ctx.Logger.Infof("TS 转封装完成: %s", outputPath)
	}

	return output, nil
}

// getVideoDuration 获取视频时长（秒）
func (s *ConvertTsStage) getVideoDuration(ctx context.Context, ffmpegPath, inputFile string) float64 {
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", inputFile,
		"-hide_banner",
	)

	output, _ := cmd.CombinedOutput()

	// 解析 Duration: HH:MM:SS.ms
	re := regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2})\.(\d{2})`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 5 {
		return 0
	}

	hours, _ := strconv.ParseFloat(matches[1], 64)
	minutes, _ := strconv.ParseFloat(matches[2], 64)
	seconds, _ := strconv.ParseFloat(matches[3], 64)
	ms, _ := strconv.ParseFloat(matches[4], 64)

	return hours*3600 + minutes*60 + seconds + ms/100
}

// getVideoCodec 探测视频流的 Codec
func (s *ConvertTsStage) getVideoCodec(ctx context.Context, ffmpegPath, inputFile string) string {
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", inputFile,
		"-hide_banner",
	)

	output, _ := cmd.CombinedOutput()
	re := regexp.MustCompile(`Video: (\w+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) < 2 {
		return ""
	}
	return strings.ToLower(matches[1])
}

// parseProgress 解析 ffmpeg 进度输出
func (s *ConvertTsStage) parseProgress(ctx context.Context, stdout io.Reader, totalDuration float64) {
	scanner := bufio.NewScanner(stdout)
	re := regexp.MustCompile(`out_time_us=(\d+)`)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) >= 2 && totalDuration > 0 {
			timeUs, _ := strconv.ParseFloat(matches[1], 64)
			currentTime := timeUs / 1000000
			progress := (currentTime / totalDuration) * 100
			_ = progress // 可以通过回调上报进度
		}
	}
}

func (s *ConvertTsStage) GetCommands() []string {
	return s.commands
}

func (s *ConvertTsStage) GetLogs() string {
	return s.logs
}
