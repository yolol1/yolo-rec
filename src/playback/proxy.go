package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bililive-go/bililive-go/src/live"
	applog "github.com/bililive-go/bililive-go/src/log"
)

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Proxy-Connection":    true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

// ProxyFLV 将 FLV 直播流实时转发给播放器，不写入磁盘。
func (m *Manager) ProxyFLV(w http.ResponseWriter, r *http.Request, token string) {
	s, err := m.getSession(token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if s.format != live.StreamFormatFLV {
		http.Error(w, ErrStreamFormatChanged.Error(), http.StatusConflict)
		return
	}

	resp, err := m.openUpstream(r.Context(), s, r.Header, false)
	if err != nil {
		http.Error(w, "获取直播上游失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		copyRawResponse(w, r, resp)
		return
	}

	copyHeaders(w.Header(), resp.Header, false)
	setFallbackContentType(w.Header(), resp.Header.Get("Content-Type"), "video/x-flv")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	copyAndFlush(w, r.Context(), s, resp.Body)
}

func (m *Manager) openUpstream(
	ctx context.Context,
	s *session,
	clientHeaders http.Header,
	forceResolve bool,
) (*http.Response, error) {
	stream, err := s.getStream(ctx, forceResolve)
	if err != nil {
		return nil, err
	}

	resp, err := m.doUpstreamRequest(ctx, stream, clientHeaders)
	if err != nil {
		return nil, err
	}
	if !forceResolve && isRetryableUpstreamStatus(resp.StatusCode) {
		resp.Body.Close()
		return m.openUpstream(ctx, s, clientHeaders, true)
	}
	return resp, nil
}

func (m *Manager) doUpstreamRequest(
	ctx context.Context,
	stream *live.StreamUrlInfo,
	clientHeaders http.Header,
) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stream.Url.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建上游播放请求失败: %w", err)
	}
	applyUpstreamHeaders(req, stream.HeadersForDownloader)
	applyClientHeaders(req, clientHeaders)
	return m.client.Do(req)
}

func applyUpstreamHeaders(req *http.Request, headers map[string]string) {
	for key, value := range headers {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Header.Set(key, value)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}
}

func applyClientHeaders(req *http.Request, headers http.Header) {
	for _, key := range []string{"Range", "If-Range"} {
		if value := headers.Get(key); value != "" {
			req.Header.Set(key, value)
		}
	}
}

func isRetryableUpstreamStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return true
	default:
		return false
	}
}

func copyRawResponse(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header, false)
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func copyHeaders(dst, src http.Header, rewritten bool) {
	connectionHeaders := connectionHeaderNames(src)
	for key, values := range src {
		canonicalKey := http.CanonicalHeaderKey(key)
		if hopByHopHeaders[canonicalKey] || connectionHeaders[canonicalKey] {
			continue
		}
		if rewritten && isRewrittenBodyHeader(canonicalKey) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func connectionHeaderNames(header http.Header) map[string]bool {
	result := make(map[string]bool)
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				result[http.CanonicalHeaderKey(name)] = true
			}
		}
	}
	return result
}

func isRewrittenBodyHeader(key string) bool {
	switch key {
	case "Content-Length", "Content-Encoding", "Content-Md5", "Etag", "Last-Modified":
		return true
	default:
		return false
	}
}

func copyAndFlush(w http.ResponseWriter, ctx context.Context, s *session, body io.Reader) {
	flusher, canFlush := w.(http.Flusher)
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		n, readErr := body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
				applog.GetLogger().Warnf("播放代理读取上游失败: live_id=%s err=%v", s.liveID, readErr)
			}
			return
		}
	}
}
