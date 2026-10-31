package playback

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/hlsproxy"
)

const maxPlaylistSize = 4 * 1024 * 1024

// ProxyHLSPlaylist 获取并重写 HLS 播放列表，使播放器通过稳定本地地址观看。
func (m *Manager) ProxyHLSPlaylist(w http.ResponseWriter, r *http.Request, token string) {
	s, err := m.getSession(token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if s.format != live.StreamFormatHLS {
		http.Error(w, ErrStreamFormatChanged.Error(), http.StatusConflict)
		return
	}

	resp, err := m.openUpstream(r.Context(), s, r.Header, false)
	if err != nil {
		http.Error(w, "获取 HLS 上游失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		copyRawResponse(w, r, resp)
		return
	}
	m.writeRewrittenPlaylist(w, r, s, resp)
}

// ProxyHLSMedia 转发 HLS 媒体分片或嵌套播放列表。
func (m *Manager) ProxyHLSMedia(w http.ResponseWriter, r *http.Request, token string) {
	s, err := m.getSession(token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if s.format != live.StreamFormatHLS {
		http.Error(w, ErrStreamFormatChanged.Error(), http.StatusConflict)
		return
	}

	target, err := s.verifyMediaRef(r.URL.Query().Get("ref"))
	if err != nil {
		http.Error(w, "无效的 HLS 媒体地址", http.StatusBadRequest)
		return
	}

	resp, err := m.openTargetWithRetry(r.Context(), s, target, r.Header)
	if err != nil {
		http.Error(w, "获取 HLS 媒体失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		copyRawResponse(w, r, resp)
		return
	}
	if isPlaylistTarget(target, resp.Header.Get("Content-Type")) {
		m.writeRewrittenPlaylist(w, r, s, resp)
		return
	}

	copyHeaders(w.Header(), resp.Header, false)
	setFallbackContentType(w.Header(), resp.Header.Get("Content-Type"), "application/octet-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		copyAndFlush(w, r.Context(), s, resp.Body)
	}
}

func (m *Manager) openTargetWithRetry(
	ctx context.Context,
	s *session,
	target *url.URL,
	clientHeaders http.Header,
) (*http.Response, error) {
	resp, err := m.doTargetRequest(ctx, s, target, clientHeaders, false)
	if err != nil {
		return nil, err
	}
	if isRetryableUpstreamStatus(resp.StatusCode) {
		resp.Body.Close()
		return m.doTargetRequest(ctx, s, target, clientHeaders, true)
	}
	return resp, nil
}

func (m *Manager) doTargetRequest(
	ctx context.Context,
	s *session,
	target *url.URL,
	clientHeaders http.Header,
	forceResolve bool,
) (*http.Response, error) {
	stream, err := s.getStream(ctx, forceResolve)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建 HLS 媒体请求失败: %w", err)
	}
	applyUpstreamHeaders(req, stream.HeadersForDownloader)
	applyClientHeaders(req, clientHeaders)
	return m.client.Do(req)
}

func (m *Manager) writeRewrittenPlaylist(
	w http.ResponseWriter,
	r *http.Request,
	s *session,
	resp *http.Response,
) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylistSize+1))
	if err != nil {
		http.Error(w, "读取 HLS 播放列表失败", http.StatusBadGateway)
		return
	}
	if len(body) > maxPlaylistSize {
		http.Error(w, "HLS 播放列表过大", http.StatusBadGateway)
		return
	}

	upstreamURL := resp.Request.URL
	rewritten, err := hlsproxy.RewritePlaylist(string(body), upstreamURL, s.buildMediaURL, false)
	if err != nil {
		http.Error(w, "重写 HLS 播放列表失败: "+err.Error(), http.StatusBadGateway)
		return
	}

	copyHeaders(w.Header(), resp.Header, true)
	setFallbackContentType(w.Header(), resp.Header.Get("Content-Type"), "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, rewritten)
	}
}

func (s *session) buildMediaURL(target *url.URL) string {
	rawURL := []byte(target.String())
	mac := hmac.New(sha256.New, s.mediaKey)
	_, _ = mac.Write(rawURL)
	ref := base64.RawURLEncoding.EncodeToString(rawURL) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("/api/playback/%s/hls/media?ref=%s", s.token, url.QueryEscape(ref))
}

func (s *session) verifyMediaRef(ref string) (*url.URL, error) {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("媒体地址签名缺失")
	}

	rawURL, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	providedMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}

	expectedMAC := hmac.New(sha256.New, s.mediaKey)
	_, _ = expectedMAC.Write(rawURL)
	if !hmac.Equal(providedMAC, expectedMAC.Sum(nil)) {
		return nil, fmt.Errorf("媒体地址签名无效")
	}

	target, err := url.Parse(string(rawURL))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("媒体地址格式无效")
	}
	return target, nil
}

func isPlaylistTarget(target *url.URL, contentType string) bool {
	path := strings.ToLower(target.Path)
	if strings.Contains(path, ".m3u8") || strings.HasSuffix(path, ".m3u") {
		return true
	}
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "mpegurl")
}

func setFallbackContentType(header http.Header, current, fallback string) {
	if strings.TrimSpace(current) == "" {
		header.Set("Content-Type", fallback)
	}
}
