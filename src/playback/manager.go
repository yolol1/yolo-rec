package playback

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
)

const (
	defaultSessionTTL         = 24 * time.Hour
	defaultResolutionCacheTTL = 20 * time.Second
	maxPlaybackSessions       = 128
)

var (
	ErrSessionNotFound     = errors.New("播放地址不存在或已过期")
	ErrStreamFormatChanged = errors.New("直播流格式已变化，请重新获取播放地址")
)

// Resolver 返回当前直播间选择后的直播流。
type Resolver func(context.Context) (*live.StreamUrlInfo, error)

// SessionInfo 是创建播放地址后返回给调用方的公开信息。
type SessionInfo struct {
	Token   string
	Format  string
	Quality string
	Name    string
}

// PlaybackPath 返回相对于服务根路径的稳定播放地址。
func (i SessionInfo) PlaybackPath() string {
	extension := live.StreamFormatFLV
	if i.Format == live.StreamFormatHLS {
		extension = "m3u8"
	}
	return fmt.Sprintf("/api/playback/%s/stream.%s", i.Token, extension)
}

type session struct {
	token        string
	liveID       string
	format       string
	quality      string
	name         string
	mediaKey     []byte
	resolve      Resolver
	createdAt    time.Time
	lastAccessAt time.Time

	resolveMu       sync.Mutex
	cachedStream    *live.StreamUrlInfo
	cachedResolveAt time.Time
	cacheTTL        time.Duration
	now             func() time.Time
}

// Manager 管理播放会话并在播放器断开时释放上游连接。
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	client   *http.Client
	now      func() time.Time
	ttl      time.Duration
	cacheTTL time.Duration
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*session),
		client:   utils.CreateDownloadClient(),
		now:      time.Now,
		ttl:      defaultSessionTTL,
		cacheTTL: defaultResolutionCacheTTL,
	}
}

func (m *Manager) Create(ctx context.Context, liveID string, resolver Resolver) (SessionInfo, error) {
	if resolver == nil {
		return SessionInfo{}, errors.New("播放流解析器不可用")
	}

	stream, err := resolver(ctx)
	if err != nil {
		return SessionInfo{}, err
	}
	if err := validateStream(stream); err != nil {
		return SessionInfo{}, err
	}

	now := m.now()
	token, err := randomToken()
	if err != nil {
		return SessionInfo{}, err
	}
	mediaKey, err := randomBytes()
	if err != nil {
		return SessionInfo{}, err
	}

	format := live.InferStreamFormat(stream)
	info := SessionInfo{
		Token:   token,
		Format:  format,
		Quality: stream.Quality,
		Name:    streamDisplayName(stream, format),
	}

	s := &session{
		token:           token,
		liveID:          liveID,
		format:          format,
		quality:         info.Quality,
		name:            info.Name,
		mediaKey:        mediaKey,
		resolve:         resolver,
		createdAt:       now,
		lastAccessAt:    now,
		cachedStream:    stream,
		cachedResolveAt: now,
		cacheTTL:        m.cacheTTL,
		now:             m.now,
	}

	m.mu.Lock()
	m.cleanupExpiredLocked(now)
	if len(m.sessions) >= maxPlaybackSessions {
		m.removeOldestSessionLocked()
	}
	m.sessions[token] = s
	m.mu.Unlock()
	return info, nil
}

// Close 释放播放代理复用的空闲连接。
func (m *Manager) Close() {
	if m.client != nil {
		m.client.CloseIdleConnections()
	}
}

func (m *Manager) getSession(token string) (*session, error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cleanupExpiredLocked(now)
	s, ok := m.sessions[token]
	if !ok {
		return nil, ErrSessionNotFound
	}
	s.lastAccessAt = now
	return s, nil
}

func (m *Manager) cleanupExpiredLocked(now time.Time) {
	for token, s := range m.sessions {
		if now.Sub(s.lastAccessAt) > m.ttl {
			delete(m.sessions, token)
		}
	}
}

func (m *Manager) removeOldestSessionLocked() {
	var oldestToken string
	var oldestAccess time.Time
	for token, s := range m.sessions {
		if oldestToken == "" || s.lastAccessAt.Before(oldestAccess) {
			oldestToken = token
			oldestAccess = s.lastAccessAt
		}
	}
	if oldestToken != "" {
		delete(m.sessions, oldestToken)
	}
}

func (s *session) getStream(ctx context.Context, force bool) (*live.StreamUrlInfo, error) {
	s.resolveMu.Lock()
	defer s.resolveMu.Unlock()

	if !force && s.cachedStream != nil && s.resolve != nil && s.now().Sub(s.cachedResolveAt) < s.cacheTTL {
		return s.cachedStream, nil
	}

	stream, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateStream(stream); err != nil {
		return nil, err
	}
	if live.InferStreamFormat(stream) != s.format {
		return nil, ErrStreamFormatChanged
	}

	s.cachedStream = stream
	s.cachedResolveAt = s.now()
	return stream, nil
}

func validateStream(stream *live.StreamUrlInfo) error {
	if stream == nil || stream.Url == nil {
		return errors.New("当前没有可用的直播流")
	}
	if !live.IsHTTPStream(stream) {
		return fmt.Errorf("当前流协议 %s 暂不支持本地播放代理", stream.Url.Scheme)
	}

	format := live.InferStreamFormat(stream)
	if format != live.StreamFormatFLV && format != live.StreamFormatHLS {
		return fmt.Errorf("当前流格式 %s 暂不支持本地播放代理", formatLabel(format))
	}
	return nil
}

func formatLabel(format string) string {
	if strings.TrimSpace(format) == "" {
		return "未知"
	}
	return format
}

func streamDisplayName(stream *live.StreamUrlInfo, format string) string {
	if strings.TrimSpace(stream.Name) != "" {
		return stream.Name
	}
	if strings.TrimSpace(stream.Quality) != "" {
		return fmt.Sprintf("%s - %s", stream.Quality, format)
	}
	return format
}

func randomToken() (string, error) {
	value, err := randomBytes()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomBytes() ([]byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("生成播放会话密钥失败: %w", err)
	}
	return value, nil
}
