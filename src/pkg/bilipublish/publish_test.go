package bilipublish

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rewriteTransport 将请求转发到本地 mock 服务器（保留原始路径和查询参数）
type rewriteTransport struct {
	target string
	base   http.RoundTripper
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	u, err := url.Parse(t.target)
	if err != nil {
		return nil, err
	}
	req2.URL.Scheme = u.Scheme
	req2.URL.Host = u.Host
	return t.base.RoundTrip(req2)
}

// TestPublishMultiPart 使用 mock 的 B站接口验证多P投稿全流程：
// 2 个视频文件 → 各自完成预上传/分片/结束上传 → 一次性提交含 2 个分P的稿件
func TestPublishMultiPart(t *testing.T) {
	// 准备两个临时视频文件
	dir := t.TempDir()
	f1 := filepath.Join(dir, "part1.flv")
	f2 := filepath.Join(dir, "part2.flv")
	assert.NoError(t, os.WriteFile(f1, []byte("fake-video-1"), 0o644))
	assert.NoError(t, os.WriteFile(f2, []byte("fake-video-2"), 0o644))

	var (
		mu              sync.Mutex
		preuploadCount  int
		partUploadCount int
		finishCount     int
		submitCount     int
		submittedBody   map[string]interface{}
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/x/vupre/web/upload/preupload":
			preuploadCount++
			uploadID := "upload-" + strconv.Itoa(preuploadCount)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"auth":       "auth-token",
					"chunk_size": 10 * 1024 * 1024, // 单分片即可传完小文件
					"endpoint":   "upos-mock.local",
					"upos_uri":   "upos://ugcf/" + uploadID + ".flv",
					"upload_id":  uploadID,
					"biz_id":     "biz-1",
				},
			})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/ugcf/"):
			partUploadCount++
			_, _ = io.Copy(io.Discard, r.Body) // 消费请求体
			w.Header().Set("ETag", `"etag-part"`)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/x/vupre/web/upload/upload":
			finishCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"cid": "cid-" + strconv.Itoa(finishCount),
				},
			})
		case r.URL.Path == "/x/vu/web/add/v3":
			submitCount++
			body, _ := io.ReadAll(r.Body)
			assert.NoError(t, json.Unmarshal(body, &submittedBody))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{"aid": 123456, "bvid": "BV1xx411c7mD"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClient("SESSDATA=abc; bili_jct=csrf-token")
	client.httpClient.Transport = &rewriteTransport{target: ts.URL, base: http.DefaultTransport}

	result, err := client.Publish(context.Background(), PublishRequest{
		Files: []PublishFile{
			{FilePath: f1, Title: "分P-1"},
			{FilePath: f2, Title: "分P-2"},
		},
		Title: "多P测试标题",
		Desc:  "多P测试简介",
		Tid:   21,
		Tags:  []string{"直播录像"},
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(123456), result.AID)
	assert.Equal(t, "BV1xx411c7mD", result.BVID)

	// 每个文件独立完成预上传/分片/结束上传，稿件只提交一次
	assert.Equal(t, 2, preuploadCount)
	assert.Equal(t, 2, partUploadCount)
	assert.Equal(t, 2, finishCount)
	assert.Equal(t, 1, submitCount)

	// 校验提交的稿件包含 2 个分P，且分P标题、cid、稿件标题正确
	videos, ok := submittedBody["videos"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, videos, 2)
	for i, v := range videos {
		item := v.(map[string]interface{})
		assert.Equal(t, "cid-"+strconv.Itoa(i+1), item["cid"])
		assert.Equal(t, "分P-"+strconv.Itoa(i+1), item["title"])
	}
	assert.Equal(t, "多P测试标题", submittedBody["title"])
	assert.Equal(t, float64(21), submittedBody["tid"])
	assert.Equal(t, "直播录像", submittedBody["tag"])
	// 默认不传 attribute（未开启不产生投稿动态时）
	_, hasAttribute := submittedBody["attribute"]
	assert.False(t, hasAttribute)
}

// TestPublishNoDynamicAndSeason 验证投稿时：
// 1. NoDynamic=true 时提交体带 attribute=2（禁止投稿动态）且 dynamic 为空
// 2. SeasonSectionID>0 时投稿成功后自动把每个分P加入合集
func TestPublishNoDynamicAndSeason(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "part1.flv")
	assert.NoError(t, os.WriteFile(f1, []byte("fake-video-1"), 0o644))

	var (
		mu             sync.Mutex
		submittedBody  map[string]interface{}
		seasonAddBody  map[string]interface{}
		seasonAddCount int
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/x/vupre/web/upload/preupload":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{
					"auth":       "auth-token",
					"chunk_size": 10 * 1024 * 1024,
					"endpoint":   "upos-mock.local",
					"upos_uri":   "upos://ugcf/season-test.flv",
					"upload_id":  "upload-season",
					"biz_id":     "biz-1",
				},
			})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/ugcf/"):
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"etag-part"`)
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/x/vupre/web/upload/upload":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{"cid": "123456"},
			})
		case r.URL.Path == "/x/vu/web/add/v3":
			body, _ := io.ReadAll(r.Body)
			assert.NoError(t, json.Unmarshal(body, &submittedBody))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"data": map[string]interface{}{"aid": 999888, "bvid": "BV1Season"},
			})
		case r.URL.Path == "/x2/creative/web/season/section/episodes/add":
			seasonAddCount++
			body, _ := io.ReadAll(r.Body)
			assert.NoError(t, json.Unmarshal(body, &seasonAddBody))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClient("SESSDATA=abc; bili_jct=csrf-token")
	client.httpClient.Transport = &rewriteTransport{target: ts.URL, base: http.DefaultTransport}

	result, err := client.Publish(context.Background(), PublishRequest{
		Files: []PublishFile{
			{FilePath: f1, Title: "分P-录制时间"},
		},
		Title:           "合集测试标题",
		Desc:            "合集测试简介",
		Tid:             21,
		Tags:            []string{"直播录像"},
		SeasonSectionID: 8081933,
		NoDynamic:       true,
	})
	assert.NoError(t, err)
	assert.Equal(t, int64(999888), result.AID)
	assert.Equal(t, "", result.Warning)

	// 不产生投稿动态：attribute=2 且 dynamic 为空
	assert.Equal(t, float64(2), submittedBody["attribute"])
	assert.Equal(t, "", submittedBody["dynamic"])

	// 投稿成功后加入合集：包含 aid/cid/分P标题
	assert.Equal(t, 1, seasonAddCount)
	assert.Equal(t, float64(8081933), seasonAddBody["sectionId"])
	episodes, ok := seasonAddBody["episodes"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, episodes, 1)
	ep := episodes[0].(map[string]interface{})
	assert.Equal(t, float64(999888), ep["aid"])
	assert.Equal(t, float64(123456), ep["cid"])
	assert.Equal(t, "分P-录制时间", ep["title"])
}
