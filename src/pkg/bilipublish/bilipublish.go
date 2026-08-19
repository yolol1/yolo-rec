package bilipublish

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
)

// B站投稿相关接口地址
const (
	apiPreupload    = "https://member.bilibili.com/x/vupre/web/upload/preupload" // 预上传（获取分片上传参数）
	apiFinishUpload = "https://member.bilibili.com/x/vupre/web/upload/upload"    // 结束上传（获取 cid）
	apiCoverUpload  = "https://member.bilibili.com/x/vu/web/cover/up"            // 封面上传
	apiSubmit       = "https://member.bilibili.com/x/vu/web/add/v3"              // 提交稿件
	apiNav          = "https://api.bilibili.com/x/web-interface/nav"             // 登录状态校验

	headerUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 bililive-go"
	headerReferer   = "https://member.bilibili.com/"

	// 上传 profile（web 端投稿）
	uploadProfile = "ugcupos/bup"
	// 客户端版本号，B站接口要求
	clientVersion = "2.10.4.0"
	clientBuild   = "2100400"

	// 分片默认大小：B站通常下发的 chunk_size 约 4MB，兜底使用 4MB
	defaultChunkSize = 4 * 1024 * 1024
	// 分片上传最大重试次数
	maxPartRetries = 3
	// B站标签数量上限
	maxTagCount = 12
	// 标题最大字数
	maxTitleRunes = 80
)

// ResolveCookie 按优先级解析投稿使用的 B站 Cookie：
// 1. BiliPublish.Cookie 显式配置
// 2. config.Cookies["www.bilibili.com"]
// 3. config.Cookies["bilibili.com"]
// 4. config.Cookies["live.bilibili.com"]
func ResolveCookie(biliPublish configs.BiliPublish, cookies map[string]string) string {
	if c := strings.TrimSpace(biliPublish.Cookie); c != "" {
		return c
	}
	for _, host := range []string{"www.bilibili.com", "bilibili.com", "live.bilibili.com"} {
		if c, ok := cookies[host]; ok && strings.TrimSpace(c) != "" {
			return strings.TrimSpace(c)
		}
	}
	return ""
}

// Client B站投稿客户端
type Client struct {
	cookie     string
	csrf       string // 从 Cookie 中解析的 bili_jct
	httpClient *http.Client
}

// NewClient 创建 B站投稿客户端
func NewClient(cookie string) *Client {
	return &Client{
		cookie: strings.TrimSpace(cookie),
		csrf:   extractCSRF(cookie),
		// 上传大文件耗时较长，不设置客户端级超时，取消依赖调用方传入的 ctx
		httpClient: &http.Client{Timeout: 0},
	}
}

// extractCSRF 从 Cookie 中解析 bili_jct 作为 CSRF 凭证
func extractCSRF(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "bili_jct=") {
			return strings.TrimPrefix(part, "bili_jct=")
		}
	}
	return ""
}

// NavResult B站登录状态信息
type NavResult struct {
	IsLogin bool   `json:"isLogin"`
	Uname   string `json:"uname"`
	Mid     int64  `json:"mid"`
}

// Verify 校验 B站 Cookie 是否有效
func (c *Client) Verify(ctx context.Context) (NavResult, error) {
	var result struct {
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Data    NavResult `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodGet, apiNav, nil, "", nil, &result); err != nil {
		return NavResult{}, fmt.Errorf("校验 B站登录状态失败: %w", err)
	}
	if result.Code != 0 || !result.Data.IsLogin {
		return NavResult{}, fmt.Errorf("B站 Cookie 未登录或已失效 (code=%d message=%s)", result.Code, result.Message)
	}
	return result.Data, nil
}

// PublishFile 单个分P的视频文件
type PublishFile struct {
	FilePath string // 视频文件路径
	FileName string // 上传文件名（含扩展名），为空则取视频文件 basename
	Title    string // 分P标题（提交时作为 videos[].title）
	Desc     string // 分P描述
}

// PublishRequest 投稿请求参数
type PublishRequest struct {
	Files     []PublishFile // 待投稿的视频文件（一个或多个，多文件将作为同一稿件的多个分P）
	FilePath  string        // 兼容单文件调用：Files 为空时使用此字段
	FileName  string        // 兼容单文件调用
	CoverPath string        // 封面文件路径（可选，为空则不传封面）
	Title     string        // 稿件标题（不超过 80 字）
	Desc      string        // 稿件简介
	Tid       int           // B站分区 tid（如 21 = 直播）
	Tags      []string      // 标签（最多 12 个）
	DTime     time.Time     // 定时发布时间，零值表示立即发布
}

// PublishResult 投稿结果
type PublishResult struct {
	AID  int64  `json:"aid"`
	BVID string `json:"bvid"`
}

// Publish 投稿视频到 B站，内部依次执行：
// 预上传 → 分片上传 → 结束上传 → 可选封面上传 → 提交稿件
func (c *Client) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	if c.cookie == "" {
		return PublishResult{}, errors.New("未配置 B站 Cookie，无法投稿")
	}
	if req.Tid <= 0 {
		return PublishResult{}, errors.New("未配置 B站投稿分区 (tid)")
	}
	if strings.TrimSpace(req.Title) == "" {
		return PublishResult{}, errors.New("投稿标题为空")
	}
	if runes := len([]rune(req.Title)); runes > maxTitleRunes {
		return PublishResult{}, fmt.Errorf("投稿标题超过 %d 字上限（当前 %d 字）", maxTitleRunes, runes)
	}
	if len(req.Tags) > maxTagCount {
		return PublishResult{}, fmt.Errorf("标签数量超过 %d 个上限（当前 %d 个）", maxTagCount, len(req.Tags))
	}

	files, err := normalizePublishFiles(req)
	if err != nil {
		return PublishResult{}, err
	}

	// 依次上传每个分P，全部成功后再统一提交稿件
	// 任一文件失败都会中断且不会提交，不会产生"部分分P"的稿件
	videos := make([]map[string]interface{}, 0, len(files))
	for i, f := range files {
		part := i + 1
		if strings.TrimSpace(f.Title) != "" {
			if runes := len([]rune(f.Title)); runes > maxTitleRunes {
				return PublishResult{}, fmt.Errorf("分P %d 标题超过 %d 字上限（当前 %d 字）", part, maxTitleRunes, runes)
			}
		}
		fileName := f.FileName
		if fileName == "" {
			fileName = filepath.Base(f.FilePath)
		}
		if fileName == "" {
			return PublishResult{}, fmt.Errorf("分P %d: 无法确定投稿文件名", part)
		}

		info, err := os.Stat(f.FilePath)
		if err != nil {
			return PublishResult{}, fmt.Errorf("分P %d: 读取视频文件失败: %w", part, err)
		}
		if info.Size() <= 0 {
			return PublishResult{}, fmt.Errorf("分P %d: 视频文件为空，无法投稿", part)
		}

		session, err := c.preupload(ctx, fileName, info.Size())
		if err != nil {
			return PublishResult{}, fmt.Errorf("分P %d: %w", part, err)
		}
		if err := c.uploadParts(ctx, f.FilePath, session); err != nil {
			return PublishResult{}, fmt.Errorf("分P %d: %w", part, err)
		}
		cid, err := c.finishUpload(ctx, session)
		if err != nil {
			return PublishResult{}, fmt.Errorf("分P %d: %w", part, err)
		}
		videos = append(videos, map[string]interface{}{
			"filename": strings.TrimPrefix(session.uposURI, "upos://"),
			"title":    f.Title,
			"desc":     f.Desc,
			"cid":      cid,
		})
	}

	// 可选封面上传：封面文件不存在时静默跳过
	coverURL := ""
	if req.CoverPath != "" {
		if _, err := os.Stat(req.CoverPath); err == nil {
			coverURL, err = c.uploadCover(ctx, req.CoverPath)
			if err != nil {
				return PublishResult{}, err
			}
		}
	}

	return c.submit(ctx, req.Title, req.Desc, req.Tid, req.Tags, req.DTime, videos, coverURL)
}

// normalizePublishFiles 规整待投稿文件列表：
// 优先使用 Files；兼容旧的单文件字段（FilePath/FileName）
func normalizePublishFiles(req PublishRequest) ([]PublishFile, error) {
	if len(req.Files) > 0 {
		return req.Files, nil
	}
	if strings.TrimSpace(req.FilePath) != "" {
		return []PublishFile{{
			FilePath: req.FilePath,
			FileName: req.FileName,
			Title:    req.Title,
			Desc:     req.Desc,
		}}, nil
	}
	return nil, errors.New("没有待投稿的视频文件")
}

// uploadSession 一次上传会话的中间状态
type uploadSession struct {
	auth      string // 分片上传鉴权
	chunkSize int64  // 分片大小（字节）
	endpoint  string // 上传端点
	uposURI   string // upos:// 形式的文件路径
	uploadID  string // 上传 ID
	bizID     string // 业务 ID
	fileName  string // 上传文件名
	fileSize  int64  // 文件大小
	chunks    int64  // 分片数量
}

// preupload 预上传：向 B站申请分片上传参数
func (c *Client) preupload(ctx context.Context, fileName string, fileSize int64) (*uploadSession, error) {
	query := url.Values{}
	query.Set("name", fileName)
	query.Set("size", strconv.FormatInt(fileSize, 10))
	query.Set("r", "upos")
	query.Set("profile", uploadProfile)
	query.Set("ssl", "1")
	query.Set("version", clientVersion)
	query.Set("build", clientBuild)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Auth      string `json:"auth"`
			ChunkSize int64  `json:"chunk_size"`
			Endpoint  string `json:"endpoint"`
			UposURI   string `json:"upos_uri"`
			UploadID  string `json:"upload_id"`
			BizID     string `json:"biz_id"`
		} `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodGet, apiPreupload, query, "", nil, &result); err != nil {
		return nil, fmt.Errorf("预上传失败: %w", err)
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("预上传失败: code=%d message=%s", result.Code, result.Message)
	}

	chunkSize := result.Data.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	session := &uploadSession{
		auth:      result.Data.Auth,
		chunkSize: chunkSize,
		endpoint:  result.Data.Endpoint,
		uposURI:   result.Data.UposURI,
		uploadID:  result.Data.UploadID,
		bizID:     result.Data.BizID,
		fileName:  fileName,
		fileSize:  fileSize,
		chunks:    (fileSize + chunkSize - 1) / chunkSize,
	}
	if session.endpoint == "" {
		return nil, errors.New("预上传响应缺少上传端点 (endpoint)")
	}
	if session.uposURI == "" {
		return nil, errors.New("预上传响应缺少 upos_uri")
	}
	return session, nil
}

// uploadParts 分片上传视频文件
func (c *Client) uploadParts(ctx context.Context, filePath string, s *uploadSession) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("打开视频文件失败: %w", err)
	}
	defer file.Close()

	baseURL := normalizeEndpoint(s.endpoint) + "/" + strings.TrimPrefix(s.uposURI, "upos://")
	for i := int64(0); i < s.chunks; i++ {
		start := i * s.chunkSize
		end := start + s.chunkSize
		if end > s.fileSize {
			end = s.fileSize
		}
		part := i + 1

		query := url.Values{}
		query.Set("partNumber", strconv.FormatInt(part, 10))
		query.Set("uploadId", s.uploadID)
		query.Set("chunk", strconv.FormatInt(i, 10))
		query.Set("chunks", strconv.FormatInt(s.chunks, 10))
		query.Set("size", strconv.FormatInt(end-start, 10))
		query.Set("start", strconv.FormatInt(start, 10))
		query.Set("end", strconv.FormatInt(end, 10))
		query.Set("total", strconv.FormatInt(s.fileSize, 10))

		if _, err := c.uploadPart(ctx, baseURL, query, file, start, end, s.auth); err != nil {
			return fmt.Errorf("分片 %d/%d 上传失败: %w", part, s.chunks, err)
		}
	}
	return nil
}

// uploadPart 上传单个分片，失败自动重试
func (c *Client) uploadPart(ctx context.Context, baseURL string, query url.Values, file *os.File, start, end int64, auth string) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxPartRetries; attempt++ {
		// 每次重试重新创建 SectionReader，避免上次读取位置影响
		reader := io.NewSectionReader(file, start, end-start)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+"?"+query.Encode(), reader)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", headerUserAgent)
		req.Header.Set("Referer", headerReferer)
		req.Header.Set("X-Upos-Auth", auth)
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = end - start
		if c.cookie != "" {
			req.Header.Set("Cookie", c.cookie)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("请求失败（第 %d 次）: %w", attempt, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		etag := strings.Trim(strings.TrimSpace(resp.Header.Get("ETag")), `"`)
		if resp.StatusCode != http.StatusOK || etag == "" {
			lastErr = fmt.Errorf("HTTP %d 且无有效 ETag（第 %d 次），响应: %s",
				resp.StatusCode, attempt, truncate(string(body), 200))
			continue
		}
		return etag, nil
	}
	return "", lastErr
}

// finishUpload 结束上传，获取文件对应的 cid
func (c *Client) finishUpload(ctx context.Context, s *uploadSession) (string, error) {
	form := url.Values{}
	form.Set("upload_id", s.uploadID)
	form.Set("name", s.fileName)
	form.Set("biz_id", s.bizID)
	form.Set("profile", uploadProfile)
	form.Set("output", "json")
	form.Set("chunk_size", strconv.FormatInt(s.chunkSize, 10))
	form.Set("chunks", strconv.FormatInt(s.chunks, 10))
	form.Set("version", clientVersion)
	form.Set("csrf", c.csrf)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			CID string `json:"cid"`
		} `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodPost, apiFinishUpload, nil, "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), &result); err != nil {
		return "", fmt.Errorf("结束上传失败: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("结束上传失败: code=%d message=%s", result.Code, result.Message)
	}
	if result.Data.CID == "" {
		return "", errors.New("结束上传响应缺少 cid")
	}
	return result.Data.CID, nil
}

// uploadCover 上传封面，返回可用的封面 URL
func (c *Client) uploadCover(ctx context.Context, coverPath string) (string, error) {
	data, err := os.ReadFile(coverPath)
	if err != nil {
		return "", fmt.Errorf("读取封面文件失败: %w", err)
	}
	coverDataURI := "data:" + mimeForCover(coverPath) + ";base64," + base64.StdEncoding.EncodeToString(data)

	form := url.Values{}
	form.Set("csrf", c.csrf)
	form.Set("cover", coverDataURI)

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodPost, apiCoverUpload, nil, "application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()), &result); err != nil {
		return "", fmt.Errorf("封面上传失败: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("封面上传失败: code=%d message=%s", result.Code, result.Message)
	}
	if result.Data.URL == "" {
		return "", errors.New("封面上传响应缺少 url")
	}
	return result.Data.URL, nil
}

// submit 提交稿件（add/v3）
func (c *Client) submit(ctx context.Context, title, desc string, tid int, tags []string, dtime time.Time, videos []map[string]interface{}, coverURL string) (PublishResult, error) {
	body := map[string]interface{}{
		"videos":             videos,
		"cover":              coverURL,
		"title":              title,
		"copyright":          1, // 自制
		"tid":                tid,
		"tag":                strings.Join(tags, ","),
		"desc_format_id":     9999,
		"desc":               desc,
		"recreate":           -1,
		"dynamic":            "",
		"interactive":        0,
		"act_reserve_create": 0,
		"no_disturbance":     0,
		"no_reprint":         1, // 禁止转载（录播默认）
		"subtitle":           map[string]interface{}{"open": 0, "lan": ""},
		"dolby":              0,
		"lossless_music":     0,
		"up_selection_reply": false,
		"up_close_reply":     false,
		"up_close_danmu":     false,
		"web_os":             3,
		"csrf":               c.csrf,
	}
	if !dtime.IsZero() {
		body["dtime"] = dtime.Format("2006-01-02 15:04:05")
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return PublishResult{}, fmt.Errorf("构建提交请求体失败: %w", err)
	}

	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AID  int64  `json:"aid"`
			BVID string `json:"bvid"`
		} `json:"data"`
	}
	if err := c.doRequest(ctx, http.MethodPost, apiSubmit, nil, "application/json",
		bytes.NewReader(payload), &result); err != nil {
		return PublishResult{}, fmt.Errorf("提交稿件失败: %w", err)
	}
	if result.Code != 0 {
		return PublishResult{}, fmt.Errorf("提交稿件失败: code=%d message=%s", result.Code, result.Message)
	}
	if result.Data.AID == 0 && result.Data.BVID == "" {
		return PublishResult{}, errors.New("提交稿件响应缺少稿件信息 (aid/bvid)")
	}
	return PublishResult{AID: result.Data.AID, BVID: result.Data.BVID}, nil
}

// doRequest 发送请求并解析 JSON 响应
func (c *Client) doRequest(ctx context.Context, method, url string, query url.Values, contentType string, body io.Reader, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if len(query) > 0 {
		req.URL.RawQuery = query.Encode()
	}
	req.Header.Set("User-Agent", headerUserAgent)
	req.Header.Set("Referer", headerReferer)
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(data), 500))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("解析响应 JSON 失败: %w，响应: %s", err, truncate(string(data), 500))
		}
	}
	return nil
}

// normalizeEndpoint 补全上传端点 scheme，返回可请求的端点地址
func normalizeEndpoint(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return "https://" + endpoint
}

// mimeForCover 根据封面文件扩展名推断 MIME 类型
func mimeForCover(coverPath string) string {
	switch strings.ToLower(filepath.Ext(coverPath)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

// truncate 截断过长的文本用于错误消息
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "…"
}
