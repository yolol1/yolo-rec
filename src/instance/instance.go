package instance

import (
	"context"
	"sync"

	"github.com/bililive-go/bililive-go/src/interfaces"
	"github.com/bluele/gcache"
)

type Instance struct {
	Ctx              context.Context // 应用级 context，不随 HTTP 请求结束而取消
	WaitGroup        sync.WaitGroup
	Lives            LiveMap
	Cache            gcache.Cache
	Server           interfaces.Module
	EventDispatcher  interfaces.Module
	ListenerManager  interfaces.Module
	RecorderManager  interfaces.Module
	PipelineManager  interfaces.Module // 后处理管道管理器
	LiveStateManager interface{}       // *livestate.Manager，使用 interface{} 避免循环依赖
	LiveStateStore   interface{}       // livestate.Store，使用 interface{} 避免循环依赖
	IOStatsModule    interfaces.Module // IO 统计模块 (*iostats.Module)
}
