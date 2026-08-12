package servers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	soop "github.com/bililive-go/bililive-go/src/live/sooplive"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/types"
)

func getLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	hostCookieMap := make(map[string]*live.InfoCookie)
	keys := make([]string, 0)
	inst.Lives.Range(func(_ types.LiveID, v live.Live) bool {
		urltmp, _ := url.Parse(v.GetRawUrl())
		if _, ok := hostCookieMap[urltmp.Host]; ok {
			return true
		}
		host := urltmp.Host
		platformName := v.GetPlatformCNName()
		if cookie, ok := configs.GetCurrentConfig().Cookies[host]; ok {
			tmp := &live.InfoCookie{Platform_cn_name: platformName, Host: host, Cookie: cookie}
			hostCookieMap[host] = tmp
		} else {
			tmp := &live.InfoCookie{Platform_cn_name: platformName, Host: host}
			hostCookieMap[host] = tmp
		}
		keys = append(keys, host)
		return true
	})
	sort.Strings(keys)
	result := make([]*live.InfoCookie, 0)
	for _, v := range keys {
		result = append(result, hostCookieMap[v])
	}
	writeJSON(writer, result)
}

func applyCookiesToLives(ctx context.Context, newCfg *configs.Config, hosts ...string) {
	inst := instance.GetInstance(ctx)
	hostSet := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		hostSet[host] = struct{}{}
	}

	for _, room := range newCfg.LiveRooms {
		tmpurl, err := url.Parse(room.Url)
		if err != nil {
			continue
		}
		if _, ok := hostSet[tmpurl.Host]; !ok {
			continue
		}

		liveObj, _ := inst.Lives.Get(room.LiveId)
		if liveObj == nil {
			inst.Lives.Range(func(_ types.LiveID, l live.Live) bool {
				if l.GetRawUrl() == room.Url {
					liveObj = l
					return false
				}
				return true
			})
		}
		if liveObj == nil {
			applog.GetLogger().Warn("can't find live by id or url: " + string(room.LiveId) + " " + room.Url)
			continue
		}
		liveObj.UpdateLiveOptionsbyConfig(ctx, &room)
	}
}

func putLiveHostCookie(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := r.Context()
	data := gjson.ParseBytes(b)

	host := data.Get("Host").Str
	cookie := data.Get("Cookie").Str
	if cookie == "" {

	} else {
		reg, _ := regexp.Compile(".*=.*")
		if !reg.MatchString(cookie) {
			writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
				ErrNo:  http.StatusBadRequest,
				ErrMsg: "cookie格式错误",
			})
			return
		}
	}
	// 使用统一 Update 接口更新 Cookies
	newCfg, err := configs.SetCookie(host, cookie)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	applyCookiesToLives(ctx, newCfg, host)
	if err := newCfg.Marshal(); err != nil {
		applog.GetLogger().Error("failed to persistence config: " + err.Error())
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// getSoopLiveAuthConfig 返回 Soop 凭证状态，供 WebUI 面板初始化使用
func getSoopLiveAuthConfig(writer http.ResponseWriter, _ *http.Request) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未加载",
		})
		return
	}

	var verifyResult *soop.CookieVerifyResult
	verifyError := ""
	cookieStatus := "missing"
	storedCookie := ""
	if cfg.Cookies != nil {
		if cookie := strings.TrimSpace(cfg.Cookies["play.sooplive.com"]); cookie != "" {
			storedCookie = cookie
		}
	}
	if storedCookie != "" {
		if result, err := soop.VerifyCookieStringCached(storedCookie); err == nil {
			verifyResult = result
			if result != nil && result.IsLogin {
				cookieStatus = "valid"
			} else {
				cookieStatus = "invalid"
				verifyError = "Soop Cookie 校验未通过，当前登录态已失效或权限不足"
			}
		} else {
			cookieStatus = "error"
			verifyError = err.Error()
		}
	}
	applog.GetLogger().Debugf("Soop auth 状态查询: hasCookie=%v cookieStatus=%s hasSavedCredential=%v verifyError=%v",
		storedCookie != "", cookieStatus, cfg.SoopLiveAuth.Username != "" || cfg.SoopLiveAuth.Password != "", verifyError != "")

	writeJSON(writer, commonResp{
		Data: map[string]any{
			"username":              cfg.SoopLiveAuth.Username,
			"has_saved_credentials": cfg.SoopLiveAuth.Username != "" || cfg.SoopLiveAuth.Password != "",
			"cookie_status":         cookieStatus,
			"verify":       verifyResult,
			"verify_error": verifyError,
		},
	})
}

// clearSoopLiveAuthConfig 清空 Soop 账号密码与持久化 Cookie
func clearSoopLiveAuthConfig(writer http.ResponseWriter, r *http.Request) {
	applog.GetLogger().Debug("Soop 清空账号密码与 Cookie 请求开始")
	newCfg, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		c.SoopLiveAuth.Username = ""
		c.SoopLiveAuth.Password = ""
		if c.Cookies != nil {
			delete(c.Cookies, "play.sooplive.com")
		}
		return nil
	}, 3, 10*time.Millisecond)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "清空 Soop 凭证失败: " + err.Error(),
		})
		return
	}

	applyCookiesToLives(r.Context(), newCfg, "play.sooplive.com")
	applog.GetLogger().Debug("Soop 清空账号密码与 Cookie 请求完成")
	writeJSON(writer, commonResp{Data: "OK"})
}

// loginSoopLive 接收前端输入的账号密码，调用 Soop 登录接口换取 Cookie
func loginSoopLive(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Username        string `json:"username"`
		Password        string `json:"password"`
		SaveCredentials bool   `json:"save_credentials"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "请求体格式错误，无法解析 Soop 登录参数"})
		return
	}
	if strings.TrimSpace(req.Username) == "" || strings.TrimSpace(req.Password) == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "Soop 账号或密码为空，请完整填写后再登录"})
		return
	}
	applog.GetLogger().Debugf("Soop Web 登录开始: username=%s saveCredentials=%v", req.Username, req.SaveCredentials)

	result, err := soop.LoginAndGetCookie(req.Username, req.Password)
	if err != nil {
		applog.GetLogger().WithError(err).Debugf("Soop Web 登录失败: username=%s", req.Username)
		writeJsonWithStatusCode(writer, http.StatusUnauthorized, commonResp{
			ErrNo:  http.StatusUnauthorized,
			ErrMsg: "Soop 登录失败，后端未能完成账号密码换 Cookie流程；常见原因包括账号密码错误、平台风控、网络异常或接口已变更: " + err.Error(),
		})
		return
	}

	newCfg, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.Cookies == nil {
			c.Cookies = make(map[string]string)
		}
		c.Cookies["play.sooplive.com"] = result.Cookie
		if req.SaveCredentials {
			c.SoopLiveAuth.Username = req.Username
			c.SoopLiveAuth.Password = req.Password
		} else {
			c.SoopLiveAuth.Username = ""
			c.SoopLiveAuth.Password = ""
		}
		return nil
	}, 3, 10*time.Millisecond)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "Soop 登录成功，但写入配置失败: " + err.Error(),
		})
		return
	}

	applyCookiesToLives(r.Context(), newCfg, "play.sooplive.com")
	applog.GetLogger().Debugf("Soop Web 登录成功: username=%s cookieLength=%d loginID=%s", req.Username, len(result.Cookie), result.Verify.LoginID)

	writeJSON(writer, commonResp{
		Data: map[string]any{
			"cookie":   result.Cookie,
			"verify":   result.Verify,
			"username": result.Username,
		},
	})
}

// verifySoopLiveCookie 验证前端提供的 Soop Cookie 是否有效
func verifySoopLiveCookie(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "请求体格式错误，无法解析 Soop Cookie"})
		return
	}
	if strings.TrimSpace(req.Cookie) == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "Soop Cookie 不能为空"})
		return
	}
	applog.GetLogger().Debugf("Soop Cookie 校验开始: cookieLength=%d", len(req.Cookie))

	result, err := soop.VerifyCookieStringCached(req.Cookie)
	if err != nil {
		applog.GetLogger().WithError(err).Debug("Soop Cookie 校验失败")
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "验证 Soop Cookie 失败；这通常表示校验接口当前不可用、网络异常，或平台返回了当前版本无法识别的响应: " + err.Error(),
		})
		return
	}
	applog.GetLogger().Debugf("Soop Cookie 校验完成: isLogin=%v loginID=%s", result.IsLogin, result.LoginID)

	writeJSON(writer, commonResp{Data: result})
}
