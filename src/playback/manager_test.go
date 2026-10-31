package playback

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bililive-go/bililive-go/src/live"
)

func TestProxyFLV(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "https://example.com/room", r.Header.Get("Referer"))
		w.Header().Set("Content-Type", "video/x-flv")
		_, _ = w.Write([]byte("flv-data"))
	}))
	defer upstream.Close()

	manager := NewManager()
	defer manager.Close()
	info, err := manager.Create(context.Background(), "live-1", staticResolver(upstream.URL, "flv"))
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, info.PlaybackPath(), nil)
	manager.ProxyFLV(recorder, request, info.Token)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "video/x-flv", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "flv-data", recorder.Body.String())
}

func TestProxyHLSPlaylistAndMedia(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "https://example.com/room", r.Header.Get("Referer"))
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		switch r.URL.Path {
		case "/live.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nsub/playlist.m3u8\n"))
		case "/sub/playlist.m3u8":
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:2,\nsegment.ts\n"))
		case "/sub/segment.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			assert.Equal(t, "bytes=0-9", r.Header.Get("Range"))
			_, _ = w.Write([]byte("segment-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	manager := NewManager()
	defer manager.Close()
	info, err := manager.Create(context.Background(), "live-2", staticResolver(upstream.URL+"/live.m3u8", "hls"))
	require.NoError(t, err)

	masterBody := requestPlaybackBody(t, manager.ProxyHLSPlaylist, info)
	subPlaylistURL := extractMediaPath(t, masterBody)
	subBody := requestPlaybackBodyAt(t, manager.ProxyHLSMedia, info, subPlaylistURL)
	segmentURL := extractMediaPath(t, subBody)
	segmentBody := requestPlaybackBodyAtWithHeaders(
		t,
		manager.ProxyHLSMedia,
		info,
		segmentURL,
		http.Header{"Range": []string{"bytes=0-9"}},
	)

	assert.Equal(t, "segment-data", segmentBody)
}

func staticResolver(target, format string) Resolver {
	return func(context.Context) (*live.StreamUrlInfo, error) {
		streamURL, err := url.Parse(target)
		if err != nil {
			return nil, err
		}
		return &live.StreamUrlInfo{
			Url:                  streamURL,
			Format:               format,
			Quality:              "原画",
			HeadersForDownloader: map[string]string{"Referer": "https://example.com/room"},
		}, nil
	}
}

func requestPlaybackBody(
	t *testing.T,
	handler func(http.ResponseWriter, *http.Request, string),
	info SessionInfo,
) string {
	t.Helper()
	return requestPlaybackBodyAt(t, handler, info, info.PlaybackPath())
}

func requestPlaybackBodyAt(
	t *testing.T,
	handler func(http.ResponseWriter, *http.Request, string),
	info SessionInfo,
	target string,
) string {
	t.Helper()
	return requestPlaybackBodyAtWithHeaders(t, handler, info, target, nil)
}

func requestPlaybackBodyAtWithHeaders(
	t *testing.T,
	handler func(http.ResponseWriter, *http.Request, string),
	info SessionInfo,
	target string,
	headers http.Header,
) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header = headers
	handler(recorder, request, info.Token)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	return recorder.Body.String()
}

func extractMediaPath(t *testing.T, playlist string) string {
	t.Helper()
	for _, line := range strings.Split(playlist, "\n") {
		if strings.HasPrefix(line, "/api/playback/") {
			return line
		}
	}
	t.Fatalf("播放列表中未找到本地媒体地址: %s", playlist)
	return ""
}
