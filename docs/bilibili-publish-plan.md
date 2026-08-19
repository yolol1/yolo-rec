# B站自动投稿功能设计方案

> 状态：规划中（v0.1）
> 目标：录制完成后，将视频自动投稿到 B站；**默认不投稿**，由用户单独指定哪些直播间投稿。

## 1. 背景与目标

项目（bililive-go）目前已具备：

- 多平台直播录制与后处理流水线（`fix_flv` / `convert_mp4` / `burn_subtitles` / `extract_cover` / `cloud_upload` / `custom_command` 等阶段）
- 录制完成后自动上传网盘（OpenList 云上传），支持上传时机、并发、限速、多存储目标
- B站 web 扫码登录能力（`/bilibili/qrcode`、`/bilibili/qrcode/poll`、`/bilibili/cookie/verify`），登录 Cookie 已持久化到 `config.Cookies`
- 房间级配置覆盖机制（`LiveRoom.OverridableConfig`）与直播间列表 UI（自动录制开关等）

本方案新增"录制完毕自动投稿 B站"能力，并同时细化云上传的房间级控制，约束如下：

1. **默认不投稿**：只有用户显式开启投稿的直播间才会触发投稿，其余直播间即使录制完成也不投稿。
2. **云上传默认跟随总开关，可单独关闭**：总开关开启后所有房间默认上传；每个直播间可单独关闭（也可强制开启）。
3. **不与现有功能冲突**：投稿与云上传互不阻断；未开启投稿/上传时流水线行为与现状完全一致。
4. **不引入新 bug**：新增配置、字段、阶段均向后兼容；对旧数据安全降级。

## 2. 现状与关键技术约束

### 2.1 流水线（Pipeline）

- 触发入口：`src/pipeline/manager.go` 的 `EnqueueRecordingTask` → `NewPipelineTask(NewRecordInfo(info), pipelineConfig, files)`。
- 阶段接口：`Stage`（`Name()` + `Execute(ctx *PipelineContext, input []FileInfo)`）。
- `PipelineContext.RecordInfo` 目前只包含 `LiveID / Platform（中文名）/ HostName / RoomName / StartTime`，**没有房间 URL**。
- 阶段注册必须同时修改 `src/pipeline/stages/register.go` 中的 `RegisterBuiltinStages` 与 `RegisterBuiltinStagesToManager` 两处，否则运行时会出现 `unknown stage` 错误。
- **执行语义**（`src/pipeline/executor.go`）：任一阶段返回 error 后，流水线立即中断，后续阶段不执行，任务标记失败。这是现有语义（云上传失败同样会中断后续阶段）。
  - 本方案为阶段新增可选 `continue_on_failure` 行为（默认关闭，现有语义不变），仅"投稿"阶段启用，使投稿失败不阻断云上传（详见 3.4）。
- 云上传阶段 `CloudUploadStage` 只读取全局配置（`configs.GetCurrentConfig()`），**不做房间级判断**，因此"指定主播"的匹配逻辑需要新建阶段自行实现。

### 2.2 配置层级

- `Config`（全局）→ `PlatformConfig`（平台级）→ `LiveRoom.OverridableConfig`（房间级），由 `ResolveConfigForRoom` 合并。
- 房间级布尔开关的现成模式：`LiveRoom.AutoRecord *bool`（`nil` 默认 true），配套 `SetLiveRoomAutoRecord(url, bool)`、后端 `enable-auto-record / disable-auto-record` 命令、前端直播间列表 Switch、SSE 广播事件。
- 配置更新接口 `updateConfig` 支持嵌套字段部分更新（已有 `cloud_upload` 先例），新增 `bili_publish` 嵌套配置可直接保存。

### 2.3 B站登录与 Cookie

- 扫码登录获取的是 B站 **web 端（passport 域）登录态**，投稿接口同样使用 web 端 Cookie（`SESSDATA` 等），因此**可以直接复用现有登录能力，无需新增登录流程**。
- Cookie 按 host 存储在 `config.Cookies`：录制侧保存/读取的是 `live.bilibili.com`；投稿接口域为 `www.bilibili.com` 或 `member.bilibili.com`。方案在读取时按优先级回退（见 3.3），不改动现有写入逻辑。

## 3. 总体设计

### 3.1 配置模型

#### 云上传房间级开关（现有功能细化，新增）

现状：`CloudUpload` 只有全局开关，无法按房间控制。新增房间级三态开关：

```go
// LiveRoom 中新增
CloudUpload *bool `yaml:"cloud_upload,omitempty" json:"cloud_upload,omitempty"`
```

语义：

| 房间级取值 | 行为 |
|---|---|
| `nil`（未配置） | 跟随全局：全局 `enable=true` 则上传，否则不上传（**现有用户行为完全不变**） |
| `true` | 强制开启（即使全局关闭也上传） |
| `false` | 强制关闭（即使全局开启也不上传） |

配套 `SetLiveRoomCloudUpload(url string, enabled *bool)`，实现与 `SetLiveRoomAutoRecord` 一致。前端直播间列表新增"上传网盘"开关，三态交互（跟随/强制开/强制关）。

> 兼容性：`CloudUploadStage` 通过 `RecordInfo.LiveURL` 查找房间；`LiveURL` 为空或房间不存在时回退"跟随全局"，因此未配置房间级开关的存量部署行为零变化。

#### 全局配置（`configs.OnRecordFinished.BiliPublish`，新增）

```yaml
on_record_finished:
  bili_publish:
    enable: false                  # 全局默认关闭；仍须房间级开启才投稿
    title_tmpl: "{{ .HostName }} {{ now | date \"2006-01-02\" }} 直播录像"
    desc_tmpl: "本视频由 bililive-go 自动录制并投稿。"
    tid: 21                        # B站分区 tid（21 = 直播，用户可改）
    tags: ["直播录像"]              # 标签，最多 12 个
    cover_use_extracted: true      # 优先使用 extract_cover 阶段生成的封面
    dtime: ""                      # 可选：定时发布（RFC3339，留空立即发布）
    delete_after: false            # 投稿成功后是否删除本地文件，默认 false
```

说明：

- `enable` 为**总开关**，房间级开关未开启时即使全局 `enable=true` 也不会投稿（双保险，安全默认）。
- 标题/简介使用现有模板函数（`utils.GetFuncMap`，与云上传路径模板同源），可引用 `{{ .HostName }}`、`{{ .RoomName }}`、`{{ .Platform }}`、`{{ now }}` 等。

#### 房间级开关（新增）

```go
// LiveRoom 中新增
BiliPublish *bool `yaml:"bili_publish,omitempty" json:"bili_publish,omitempty"` // nil = 不投稿
```

- 匹配语义：`room.BiliPublish == nil || !*room.BiliPublish` → 跳过投稿（**安全默认：没有显式开启就跳过**）。
- 配套 `SetLiveRoomBiliPublish(url string, publish bool)`，实现与 `SetLiveRoomAutoRecord` 完全一致。
- 默认配置（`defaultConfig`）中 `BiliPublish{Enable: false}`。

### 3.2 "指定主播"匹配机制

投稿阶段需要知道"这次录制属于哪个房间"。现状 `RecordInfo` 缺房间 URL，因此：

1. `RecordInfo` 新增字段 `LiveURL string`（`NewRecordInfo` 中从 `info.Live.GetRawUrl()` 获取）。
2. 旧数据兼容：历史持久化任务中 `LiveURL` 为空 → 投稿阶段直接跳过（安全降级，不影响旧任务重试）。
3. 匹配逻辑（在 `BiliPublishStage.Execute` 内）：

```text
平台 != bilibili                    → 跳过（正常返回）
LiveURL 为空                        → 跳过
config.GetLiveRoomByUrl(LiveURL) 失败 → 跳过（房间已删除等）
room.BiliPublish == nil || !*room.BiliPublish → 跳过
全局 BiliPublish.Enable == false    → 跳过
```

所有"跳过"均为正常返回（不报错、不影响任务状态、不影响后续阶段）；只有真正开始投稿后的失败才标记任务失败。

### 3.3 B站投稿客户端（新增 `src/pkg/bilipublish`）

新包封装 web 投稿流程，接口层与流水线解耦，便于单测与替换：

```go
type Client struct { cookie string } // cookie 从全局配置解析
type PublishRequest struct {
    VideoPath string
    CoverPath string // 可选
    Title, Desc string
    Tid int
    Tags []string
    DTime time.Time  // 零值 = 立即发布
}
func (c *Client) Verify(ctx) (*AccountInfo, error)  // 校验登录态（/x/web-interface/nav）
func (c *Client) Publish(ctx, req PublishRequest) (bvID string, err error)
```

Cookie 解析优先级（避免改动现有存储与登录 UI）：

```text
config.Cookies["www.bilibili.com"] → config.Cookies["bilibili.com"] → config.Cookies["live.bilibili.com"]
```

若 `BiliPublish.Cookie` 显式配置则优先使用。Cookie 无效/过期时返回明确错误（提示重新扫码登录），并复用现有通知渠道告知用户。

投稿流程（web 端接口，实现阶段需实测确认）：

1. 预检/获取上传凭据（`member.bilibili.com/x/web/archive/pre` 等）
2. 分片上传视频到 upos 存储（分片 PUT + 合并完成）
3. 上传封面（如启用且存在封面文件）
4. 提交稿件（标题/简介/分区/标签/封面/定时发布）
5. 返回 BV 号，写入阶段日志与任务结果

> 注意：B站 web 投稿接口可能有风控（验证码）、Cookie 失效、分区限制等情况；方案不做自动化绕过，失败即如实上报。分片上传为较重的实现，MVP 建议支持断点续传前先保证单次上传可靠、失败任务可在任务队列手动重试。

### 3.4 新流水线阶段 `bili_publish`

新增 `src/pipeline/stages/bili_publish.go`，结构参考 `CloudUploadStage`（含 `GetCommands()` / `GetLogs()`，失败详情在任务队列页可见）。

行为：

- 输入过滤：只处理 `FileTypeVideo`；`FileTypeCover` 仅作为封面候选；文件不存在则跳过并记录警告（不视为失败）。
- 匹配失败/未启用：正常返回 input（原样透传），保证后续阶段不受影响。
- 投稿成功：记录 BV 号与耗时；默认**不删除**任何文件（`delete_after=false`）。
- 投稿失败：返回 error，但因阶段启用了 `continue_on_failure`，**不会阻断后续云上传与自定义命令**；任务最终仍标记失败，日志详细，可在任务队列重试。
- 并发/限速：投稿为单文件串行即可，MVP 不做多任务并发控制；上传速度限制可作为后续增强。

阶段顺序（在 `ConvertLegacyConfig` 中的插入位置）：

```text
fix_flv → convert_mp4 → burn_subtitles → extract_cover → bili_publish → cloud_upload → custom_command
```

理由：

- **先投稿、后云上传**：云上传可能配置 `delete_after_upload=true`（上传后删除本地文件），投稿必须在此之前完成，否则无源文件可投。
- **投稿失败不阻断云上传**：投稿阶段启用 `continue_on_failure`，投稿失败时云上传、自定义命令照常执行，任务标记失败（日志可见、可重试）。这是对"两个功能互不破坏"的硬保证。
- 云上传若因房间级开关被跳过，投稿不受影响。

#### Pipeline 失败继续语义（新增，`continue_on_failure`）

`src/pipeline/types.go` 的 `StageConfig` 新增可选字段：

```go
ContinueOnFailure *bool `yaml:"continue_on_failure,omitempty" json:"continue_on_failure,omitempty"`
func (sc *StageConfig) ShouldContinueOnFailure() bool { ... } // nil 默认 false
```

`src/pipeline/executor.go` 调整：

- 阶段失败时，若 `ShouldContinueOnFailure()` 为 false：行为与现状完全一致（记录失败结果并立即返回错误）。
- 为 true：记录该阶段失败结果（`StageStatusFailed` + 错误信息），**继续执行后续阶段**；全部阶段执行完后，若存在失败阶段，返回聚合错误（如 `N 个阶段失败: bili_publish: ...`）。
- 任务状态判定不变（`manager.executeTask` 根据返回 err 标记失败/完成），因此有失败阶段的任务必然标记失败，不会误报成功。

> 该能力默认关闭，只对显式开启的阶段生效；现有所有阶段的默认行为不变，不构成回归风险。需补充单测覆盖：`continue_on_failure=true` 时后续阶段照常执行且任务最终失败。

### 3.5 迁移与注册

1. `src/pipeline/config.go`：
   - `OnRecordFinishedPipeline` 增加 `BiliPublish configs.BiliPublish` 字段。
   - `ConvertLegacyConfig` 在云上传之后、自定义命令之前追加：
     ```go
     if legacy.BiliPublish.Enable {
         stages = append(stages, StageConfig{
             Name:               StageNameBiliPublish,
             ContinueOnFailure:  pipeline.BoolPtr(true), // 投稿失败不阻断云上传
             Options: map[string]any{
                 OptionTitleTmpl:      legacy.BiliPublish.TitleTmpl,
                 OptionDescTmpl:       legacy.BiliPublish.DescTmpl,
                 OptionTid:            legacy.BiliPublish.Tid,
                 OptionTags:           legacy.BiliPublish.Tags,
                 OptionDTime:          legacy.BiliPublish.DTime,
                 OptionDeleteAfter:    legacy.BiliPublish.DeleteAfter,
                 OptionCoverExtracted: legacy.BiliPublish.CoverUseExtracted,
             },
         })
     }
     ```
   - 云上传阶段的插入条件由 `Enable && StorageName != ""` 调整为 `StorageName != ""`（只要有存储目标即插入阶段），是否实际上传由阶段内"全局开关 + 房间级三态"最终判定。这样"房间级强制开启"在全局关闭时也能生效；未配置任何存储目标时仍不插入（现状不变）。
   - 新增阶段名常量 `StageNameBiliPublish = "bili_publish"` 与选项键常量。
2. `src/pipeline/stages/register.go`：**两处**注册函数都增加 `executor.RegisterStage(pipeline.StageNameBiliPublish, NewBiliPublishStage)`。
3. `src/configs/config.go`：新增 `BiliPublish` 结构、默认值、`SetLiveRoomBiliPublish`、`SetLiveRoomCloudUpload`，`LiveRoom` 新增 `BiliPublish *bool` 与 `CloudUpload *bool` 字段，并在配置注释（`config_comments.go`）与 `config.yml` 示例中补充说明。
4. `src/live/info.go`：`Info` 增加 `BiliPublish bool`（由房间配置解析填充），`MarshalJSON` 同步输出，供前端列表展示开关状态（与 `auto_record` 同模式）。
   - 同时增加 `CloudUploadEnabled bool`（房间级生效后的实际云上传状态：跟随全局或强制值），供前端展示"上传网盘"开关的当前生效状态。

### 3.6 后端 API

- 复用现有直播间操作命令通道（`src/servers/handler.go`）新增：
  - `enable-bili-publish` / `disable-bili-publish`：调用 `configs.SetLiveRoomBiliPublish(live.GetRawUrl(), true/false)`，并广播 SSE 事件（`bili_publish_enable` / `bili_publish_disable`），模式与 `auto_record` 一致。
  - `enable-cloud-upload` / `disable-cloud-upload` / `reset-cloud-upload`：调用 `configs.SetLiveRoomCloudUpload`（强制开/强制关/跟随全局），广播 SSE 事件。
- 配置保存：`updateConfig` 已支持嵌套字段，前端提交 `on_record_finished.bili_publish` 即可，无需新端点。
- 登录态校验：复用现有 `/bilibili/qrcode` 与 `/bilibili/cookie/verify`（验证成功返回 UID/等级，可顺带展示投稿可用状态）。

### 3.7 前端

1. **全局设置卡片**（新增 `src/webapp/src/component/config-info/BiliPublishSettings.tsx`，参考 `CloudUploadSettings.tsx`）：
   - 启用总开关、标题/简介模板输入、分区选择（tid）、标签编辑、定时发布、投稿后删除本地文件
   - 内嵌 B站登录状态（复用 `BiliLoginPanel`，提示"投稿与录制共用同一 Cookie"）
2. **直播间列表**（`live-list/index.tsx`）：
   - 增加"上传网盘"开关（三态：跟随全局/强制开/强制关），展示生效状态 `item.cloud_upload_enabled`。
   - 增加"投稿 B站"开关，仅在平台为 B站时显示；调用 `api.enableBiliPublish / api.disableBiliPublish`，展示 `item.bili_publish`。
3. **任务队列页**：阶段名映射补充 `'bili_publish': 'B站投稿'`（`pipeline-task-list/index.tsx`）。

### 3.8 通知（可选，MVP 可不做）

投稿成功/失败可接入现有通知（Telegram / Bark / Email / Ntfy），事件源为任务更新事件。建议 MVP 只做日志 + 任务状态，通知作为后续增强，避免扩大改动面。

## 4. 与现有功能的边界（不冲突策略）

| 现有功能 | 冲突点 | 规避措施 |
|---|---|---|
| 云上传 | 上传后删除本地文件会使投稿无源文件 | **顺序改为投稿在前、云上传在后**；投稿阶段对"文件不存在"仍防御性跳过 |
| 云上传 | 投稿失败会阻断云上传 | 投稿阶段启用 `continue_on_failure`，失败不中断后续阶段，任务仍标记失败 |
| 云上传房间级开关 | 修改现有 `CloudUploadStage` | 房间级三态仅在显式配置时生效；`LiveURL` 缺失/房间不存在回退跟随全局，存量行为零变化 |
| 弹幕烧录/转码/封面 | 封面可共用 | 投稿只读取 `extract_cover` 输出的封面，不修改/不删除 |
| 自定义命令 | 投稿失败会中断其执行 | 投稿阶段 `continue_on_failure` 同样保护自定义命令 |
| Cookie 存储 | 投稿需 web Cookie | 只读复用现有 `config.Cookies`，按优先级回退，不新增重复存储 |
| 任务持久化 | `RecordInfo` 加字段 | JSON 兼容，旧数据空值安全跳过 |
| 配置解析 | 新增字段 | `omitempty` + 默认关闭，旧配置文件可原样加载 |

## 5. 兼容性与风险控制

- **默认关闭**：投稿全局 `enable=false`、房间级 `nil=false`、阶段仅在显式开启时插入，三重保险确保不改变现有用户行为。
- **云上传默认跟随**：房间级 `nil` 跟随全局，存量用户（未配置房间级开关）行为与现状完全一致。
- **旧数据兼容**：`RecordInfo.LiveURL` 为空时跳过投稿；`LiveRoom.BiliPublish` 为 nil 时不投稿；旧 pipeline 任务重试时不会意外触发投稿。
- **注册完整性**：两处阶段注册同步修改，避免 `unknown stage` 运行时错误。
- **失败语义透明**：跳过 ≠ 失败；投稿真实失败时任务标记失败但后续阶段照常执行，任务队列页可重试。
- **`continue_on_failure` 默认关闭**：只对显式开启的阶段生效，现有阶段失败中断语义不变。
- **不修改现有阶段**：`CloudUploadStage` 等现有代码保持不动，新增逻辑全部落在新文件/新字段。

## 6. 实施步骤（按依赖排序）

1. `configs`：`BiliPublish` 结构 + 默认值 + `LiveRoom.BiliPublish` / `LiveRoom.CloudUpload` + `SetLiveRoomBiliPublish` / `SetLiveRoomCloudUpload` + 配置注释/示例
2. `pipeline`：`RecordInfo.LiveURL` + `NewRecordInfo` 填充
3. `pipeline`：`StageConfig.ContinueOnFailure` + executor 失败继续语义 + 单测
4. `stages`：`CloudUploadStage` 接入房间级三态判定（先做，因投稿阶段依赖同套匹配）；`bili_publish.go` 新建
5. `pkg/bilipublish`：客户端（登录态校验、上传、提交），先用测试账号实测接口
6. `register.go` 两处注册 + `pipeline/config.go` 常量/迁移（含云上传插入条件调整）
7. `servers`：`enable/disable-bili-publish`、`enable/disable/reset-cloud-upload` 命令 + SSE 事件；`live/info.go` 透传开关状态
8. 前端：`BiliPublishSettings.tsx` + 直播间两个开关 + `api.ts` + 任务队列阶段名
9. 测试：单测（配置合并、匹配逻辑、模板渲染、`ConvertLegacyConfig` 迁移、`continue_on_failure`）、E2E（开关 UI 与保存）、手动验证真实投稿
10. 按 `AGENTS.md` 要求执行 `make build-web dev`、`make lint`、`make test`，通过后提交

## 7. 测试计划

单元测试：

- `configs`：`BiliPublish` 默认值、`SetLiveRoomBiliPublish`、`LiveRoom.BiliPublish` nil 语义
- `configs`：`SetLiveRoomCloudUpload` 三态语义、`CloudUploadStage` 房间级判定（nil 跟随全局 / true 强制开 / false 强制关、房间缺失回退全局）
- `pipeline`：`RecordInfo` 序列化兼容（旧 JSON 无 `LiveURL`）、`ConvertLegacyConfig` 迁移顺序
- `pipeline`：`continue_on_failure` 失败继续语义（后续阶段照常执行、任务最终失败、默认行为不变）
- `stages`：B站平台才投稿、非 B站跳过、房间未开启跳过、`LiveURL` 为空跳过、文件不存在跳过、标题模板渲染
- `stages`：云上传在房间关闭时跳过、房间强制开启时全局关闭也上传、未配置房间级开关时跟随全局（回归保护）
- `bilipublish`：Cookie 解析优先级、请求构造（mock HTTP）

E2E：

- 直播间列表出现"投稿 B站"开关且仅 B站房间可见
- 直播间列表"上传网盘"三态开关切换后配置保存并刷新保持
- 开关切换后配置保存并刷新保持
- 全局设置卡片保存 `bili_publish` 配置

手动验证（必须）：

- 使用测试账号完成一次真实投稿，验证标题/简介/分区/标签/封面/定时发布
- 验证投稿失败时任务标记失败、日志可见、可重试
- 验证先投稿后云上传的顺序；开启 `delete_after_upload` 时投稿已完成不受影响
- 验证云上传失败时已完成的投稿不受影响（阶段独立）
- 验证云上传房间级开关：全局开启 + 房间关闭 → 该房间不上传；全局关闭 + 房间强制开启 → 该房间上传

## 8. 待验证事项

- B站 web 投稿接口（预检、分片上传、提交）在当前日期的可用性与字段要求（实现第 3 步前用测试账号实测）
- 分区 `tid` 完整列表与默认值
- 定时发布最小时间限制（B站要求不早于当前时间 2 小时）
- 投稿账号的风控/权限限制（未实名、未绑定手机等场景的错误处理）
- Cookie 有效期与失效后的自动提醒路径
