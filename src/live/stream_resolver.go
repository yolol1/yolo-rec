package live

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bililive-go/bililive-go/src/configs"
)

const (
	StreamFormatFLV  = "flv"
	StreamFormatHLS  = "hls"
	StreamFormatRTMP = "rtmp"
)

// ResolveStreamInfos 统一获取直播流信息，并兼容只实现旧接口的平台。
func ResolveStreamInfos(l Live) ([]*StreamUrlInfo, error) {
	infos, err := l.GetStreamInfos()
	if err == nil {
		return infos, nil
	}
	if !errors.Is(err, ErrNotImplemented) {
		return nil, err
	}

	//nolint:staticcheck // 兼容尚未迁移到 GetStreamInfos 的平台
	urls, urlErr := l.GetStreamUrls()
	if urlErr != nil {
		if errors.Is(urlErr, ErrNotImplemented) {
			return nil, fmt.Errorf("当前平台未实现直播流解析")
		}
		return nil, urlErr
	}

	result := make([]*StreamUrlInfo, 0, len(urls))
	for _, u := range urls {
		if u == nil {
			continue
		}
		result = append(result, &StreamUrlInfo{
			Url:                  u,
			HeadersForDownloader: make(map[string]string),
		})
	}
	return result, nil
}

// SelectPreferredStream 按直播间配置选择播放或录制使用的流。
func SelectPreferredStream(l Live, streamInfos []*StreamUrlInfo) *StreamUrlInfo {
	if len(streamInfos) == 0 {
		return nil
	}

	firstValid := firstStreamWithURL(streamInfos)
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		return firstValid
	}

	preference := cfg.GetEffectiveConfigForRoom(l.GetRawUrl()).StreamPreference
	if preference.Quality == nil && preference.Attributes == nil {
		return firstValid
	}

	var quality string
	if preference.Quality != nil {
		quality = *preference.Quality
	}
	var attrs map[string]string
	if preference.Attributes != nil {
		attrs = *preference.Attributes
	}

	logUnavailableAttributeKeys(l, streamInfos, attrs)

	best := findBestMatchingStream(streamInfos, quality, attrs)
	if best != nil {
		return best
	}

	if firstValid != nil {
		l.GetLogger().Warnf("没有流匹配配置的偏好 (quality=%s, attrs=%v)，使用第一个可用流", quality, attrs)
	}
	return firstValid
}

func firstStreamWithURL(streamInfos []*StreamUrlInfo) *StreamUrlInfo {
	for _, info := range streamInfos {
		if info != nil && info.Url != nil {
			return info
		}
	}
	return nil
}

func findBestMatchingStream(streamInfos []*StreamUrlInfo, quality string, attrs map[string]string) *StreamUrlInfo {
	var best *StreamUrlInfo
	bestScore := 0
	for _, info := range streamInfos {
		if info == nil || info.Url == nil {
			continue
		}

		score := 0
		if quality != "" && info.Quality == quality {
			score += 100
		}
		for key, value := range attrs {
			if info.AttributesForStreamSelect[key] == value {
				score++
			}
		}
		if score > bestScore {
			best = info
			bestScore = score
		}
	}
	return best
}

func logUnavailableAttributeKeys(l Live, streamInfos []*StreamUrlInfo, attrs map[string]string) {
	if len(attrs) == 0 {
		return
	}

	availableKeysMap := make(map[string]bool)
	for _, info := range streamInfos {
		if info == nil || info.Url == nil {
			continue
		}
		for key := range info.AttributesForStreamSelect {
			availableKeysMap[key] = true
		}
	}

	availableKeys := make([]string, 0, len(availableKeysMap))
	for key := range availableKeysMap {
		availableKeys = append(availableKeys, key)
	}
	sort.Strings(availableKeys)

	if len(availableKeys) == 0 {
		l.GetLogger().Warnf("当前平台(%s)未提供任何用于筛选的流属性，您的属性偏好配置 %v 将无法生效", l.GetPlatformCNName(), attrs)
		return
	}

	for key := range attrs {
		if !availableKeysMap[key] {
			l.GetLogger().Warnf("检测到偏好设置了在当前平台(%s)流属性中不存在的键 '%s'，可能无法生效。当前实际支持的属性键有: %v，请检查拼写是否正确", l.GetPlatformCNName(), key, availableKeys)
		}
	}
}

// InferStreamFormat 根据流元数据或 URL 推断播放器可识别的格式。
func InferStreamFormat(info *StreamUrlInfo) string {
	if info == nil {
		return ""
	}

	format := strings.ToLower(strings.TrimSpace(info.Format))
	switch format {
	case StreamFormatFLV, StreamFormatHLS, StreamFormatRTMP:
		return format
	}
	if info.Url == nil {
		return format
	}

	path := strings.ToLower(info.Url.Path)
	switch {
	case strings.Contains(path, ".m3u8") || strings.HasSuffix(path, ".m3u"):
		return StreamFormatHLS
	case strings.Contains(path, ".flv"):
		return StreamFormatFLV
	case info.Url.Scheme == "rtmp" || info.Url.Scheme == "rtmps":
		return StreamFormatRTMP
	default:
		return format
	}
}

// IsHTTPStream 判断流地址是否可由 HTTP 播放代理转发。
func IsHTTPStream(info *StreamUrlInfo) bool {
	if info == nil || info.Url == nil {
		return false
	}
	return info.Url.Scheme == "http" || info.Url.Scheme == "https"
}
