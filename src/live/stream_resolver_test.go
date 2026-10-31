package live_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/mock"
)

func TestResolveStreamInfosFallsBackToLegacyURLs(t *testing.T) {
	ctrl := gomock.NewController(t)
	l := mock.NewMockLive(ctrl)
	streamURL, err := url.Parse("https://example.com/live.flv")
	require.NoError(t, err)

	l.EXPECT().GetStreamInfos().Return(nil, live.ErrNotImplemented)
	//nolint:staticcheck // 覆盖旧平台的兼容解析路径
	l.EXPECT().GetStreamUrls().Return([]*url.URL{streamURL}, nil)

	infos, err := live.ResolveStreamInfos(l)

	require.NoError(t, err)
	require.Len(t, infos, 1)
	assert.Equal(t, streamURL, infos[0].Url)
	assert.Empty(t, infos[0].HeadersForDownloader)
}

func TestInferStreamFormat(t *testing.T) {
	flvURL, _ := url.Parse("https://example.com/live.flv?token=1")
	hlsURL, _ := url.Parse("https://example.com/live/playlist")

	assert.Equal(t, "flv", live.InferStreamFormat(&live.StreamUrlInfo{Url: flvURL}))
	assert.Equal(t, "hls", live.InferStreamFormat(&live.StreamUrlInfo{Url: hlsURL, Format: "HLS"}))
	assert.Equal(t, "rtmp", live.InferStreamFormat(&live.StreamUrlInfo{Url: &url.URL{Scheme: "rtmp", Host: "example.com"}}))
}
