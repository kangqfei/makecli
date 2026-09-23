/**
 * [INPUT]: 依赖 context、log/slog、sync、time；传输来自 client.go，执行编排来自 run.go，执行契约来自 adapter 包
 * [OUTPUT]: 对外提供 Daemon 与 Options、Run（node key 续连或 setup-key 入册 → 心跳 goroutine → claim 轮询 → 串行执行）
 * [POS]: internal/daemon 的主循环——正确性完全建立在拉取式 claim 上，连接断开只影响延迟；
 *        取消指令随心跳 actions 下发，v1 单设备串行执行（claim max=1）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qfeius/makecli/internal/daemon/adapter"
)

// 协议节拍（agent-design/docs/contracts/internal.md §7）。
const (
	heartbeatInterval = 15 * time.Second
	claimInterval     = 3 * time.Second
	// DefaultMaxRunDuration 是设备侧时长兜底——平台不 wall-clock 杀心跳
	// 存活的 run，卡死的 CLI 由设备自己终止。
	DefaultMaxRunDuration = time.Hour
)

// Options 是 daemon 的启动配置。
type Options struct {
	ServerURL      string
	NodeKey        string // 已入册 runtime 的长期身份
	SetupKey       string // 未入册 runtime 使用的一次性 setup-key；与 NodeKey 互斥
	RuntimeName    string
	WorkBaseDir    string        // 工作目录根，默认 ~/.make/agent/work
	MaxRunDuration time.Duration // 0 取 DefaultMaxRunDuration
	Backends       []adapter.Backend
	Logger         *slog.Logger
}

// Daemon 是外接 brain 的接入点：入册 runtime、心跳续活、claim 领工作、
// 驱动 CLI 执行并回写事件流。
type Daemon struct {
	client         *Client
	backends       map[string]adapter.Backend // provider → backend（已探测可用）
	capabilities   []RuntimeCapability
	runtimeName    string
	nodeKey        string
	workBaseDir    string
	maxRunDuration time.Duration
	logger         *slog.Logger

	mu              sync.Mutex
	activeRun       string             // 当前执行中的 run_id（空=空闲）
	cancelRun       context.CancelFunc // 取消当前 run
	wasCancled      *atomic.Bool       // 当前执行的取消标记
	activeExecution string
}

// New 探测各 backend 可用性并构造 Daemon；一个可用 backend 都没有即报错。
func New(ctx context.Context, options Options) (*Daemon, error) {
	if options.ServerURL == "" {
		return nil, fmt.Errorf("server URL 必填（--gateway-server-url）")
	}
	if options.NodeKey == "" && options.SetupKey == "" {
		return nil, fmt.Errorf("首次接入需 --setup-key；已入册则读本地 node key")
	}
	if options.NodeKey != "" && options.SetupKey != "" {
		return nil, fmt.Errorf("node key 与 setup-key 不能同时使用")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.MaxRunDuration <= 0 {
		options.MaxRunDuration = DefaultMaxRunDuration
	}
	daemon := &Daemon{
		client:         NewClient(options.ServerURL, options.NodeKey),
		backends:       map[string]adapter.Backend{},
		runtimeName:    options.RuntimeName,
		nodeKey:        options.NodeKey,
		workBaseDir:    options.WorkBaseDir,
		maxRunDuration: options.MaxRunDuration,
		logger:         options.Logger,
	}
	for _, backend := range options.Backends {
		version, err := backend.Detect(ctx)
		if err != nil {
			options.Logger.Warn("provider 不可用,跳过", "provider", backend.Provider(), "err", err)
			continue
		}
		daemon.backends[backend.Provider()] = backend
		daemon.capabilities = append(daemon.capabilities, RuntimeCapability{Provider: backend.Provider(), Version: version})
		options.Logger.Info("provider 就绪", "provider", backend.Provider(), "version", version)
	}
	if len(daemon.backends) == 0 {
		return nil, fmt.Errorf("没有可用的 brain CLI（claude / codex 均未探测到）")
	}
	// 无 node key：用 setup-key 首次入册，换回长期 node key
	// （capabilities 随入册上报）。setup-key 一次性消费；正常重启不再传它。
	if daemon.nodeKey == "" {
		enrolled, err := daemon.client.EnrollRuntime(ctx, CreateRuntimeRequest{
			SetupKey: options.SetupKey, Name: daemon.runtimeName, Capabilities: daemon.capabilities,
		})
		if err != nil {
			return nil, fmt.Errorf("入册失败: %w", err)
		}
		daemon.nodeKey = enrolled.NodeKey
		daemon.client.SetToken(enrolled.NodeKey)
		daemon.logger.Info("runtime enrolled", "runtime_id", enrolled.RuntimeID)
	}
	return daemon, nil
}

// NodeKey 返回当前 node key——cmd 层据此判断是否需要落盘持久化（入册换回的新 key）。
func (d *Daemon) NodeKey() string { return d.nodeKey }

// Run 阻塞运行直到 ctx 取消：注册 → 心跳 goroutine → claim 轮询（3s）。
// v1 单设备串行：执行中不 claim，新消息在平台侧排队为下一个 run。
func (d *Daemon) Run(ctx context.Context) error {
	// 入册已在 New 完成（换回 node key）；此处只驱动心跳 + claim 双循环。
	d.logger.Info("runtime online", "capabilities", len(d.capabilities))

	go d.heartbeatLoop(ctx)

	providers := make([]string, 0, len(d.backends))
	for provider := range d.backends {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	ticker := time.NewTicker(claimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if d.busy() {
				continue
			}
			// 按 provider 分别 claim：RunClaim 不携带 provider 字段，
			// 单能力请求领到即知道该用哪个 CLI——匹配不靠猜。
			for _, provider := range providers {
				claims, err := d.client.ClaimRuns(ctx, CreateRunClaimRequest{Capabilities: []string{provider}, Max: 1})
				if err != nil {
					d.logger.Warn("claim", "provider", provider, "err", err)
					continue
				}
				if len(claims) > 0 {
					d.launch(ctx, d.backends[provider], claims[0])
					break
				}
			}
		}
	}
}

// launch 启动一个 run 的执行 goroutine 并登记取消入口。
func (d *Daemon) launch(ctx context.Context, backend adapter.Backend, claim RunClaim) {
	runCtx, cancel := context.WithCancel(ctx)
	var cancelled atomic.Bool
	d.mu.Lock()
	d.activeRun = claim.RunID
	if claim.Execution != nil {
		d.activeExecution = claim.Execution.Execution.ID
	}
	d.cancelRun = cancel
	d.wasCancled = &cancelled
	d.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			d.mu.Lock()
			d.activeRun = ""
			d.activeExecution = ""
			d.cancelRun = nil
			d.wasCancled = nil
			d.mu.Unlock()
		}()
		d.executeRun(runCtx, backend, claim, &cancelled)
	}()
}

// busy 报告是否有执行中的 run。
func (d *Daemon) busy() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.activeRun != ""
}

// heartbeatLoop 15s 心跳：续活在线状态，消费 actions 取消指令。
func (d *Daemon) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			response, err := d.client.Heartbeat(ctx, UpdateRuntimeRequest{Capabilities: d.capabilities})
			if err != nil {
				d.logger.Warn("heartbeat", "err", err)
				continue
			}
			for _, action := range response.Actions {
				if action.Kind == "cancel_run" {
					d.cancelActiveRun(action.RunID)
				}
				if action.Kind == "cancel_execution" {
					d.cancelActiveExecution(action.ExecutionID)
				}
			}
		}
	}
}

// cancelActiveRun 终止指定 run 的执行——设备随后以 FailRun(cancelled) 收尾。
func (d *Daemon) cancelActiveRun(runID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.activeRun != runID || d.cancelRun == nil {
		return // 已结束或不是本设备的活（幂等忽略）
	}
	d.logger.Info("收到取消指令,终止执行", "run", runID)
	d.wasCancled.Store(true)
	d.cancelRun()
}

func (d *Daemon) cancelActiveExecution(executionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.activeExecution != executionID || d.cancelRun == nil {
		return
	}
	d.logger.Info("收到执行取消指令", "execution", executionID)
	d.wasCancled.Store(true)
	d.cancelRun()
}
