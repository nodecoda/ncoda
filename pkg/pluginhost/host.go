package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nodecoda/ncoda/pkg/runtime/plugin"
)

// Options 控制宿主进程。
type Options struct {
	// ArtifactDir 传给插件的产物目录（NCODA_ARTIFACT_DIR）；空 = 不创建。
	ArtifactDir string
	// RuntimeName/Version 写进 initialize 握手（诊断用）。
	RuntimeName    string
	RuntimeVersion string
	// StartTimeout 覆盖握手超时（默认 10s）。
	StartTimeout time.Duration
}

// Host 管理一个插件进程：启动、握手、调用、重启、关闭。
type Host struct {
	manifest *Manifest
	opts     Options

	mu       sync.Mutex
	proc     *process
	restarts []time.Time
	disabled error
	closed   bool
}

// New 构造宿主（不启动进程）。
func New(m *Manifest, opts Options) *Host {
	if opts.RuntimeName == "" {
		opts.RuntimeName = "ncoda-run"
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = 10 * time.Second
	}
	return &Host{manifest: m, opts: opts}
}

// Manifest 返回宿主对应的清单。
func (h *Host) Manifest() *Manifest { return h.manifest }

// Symbols 返回清单声明的符号。
func (h *Host) Symbols() []string { return h.manifest.Symbols() }

// Register 启动/握手并把符号注册进 Manager（含 `<lib>/<fn>` 别名）。
// 重复符号会让 Register 失败——外部实现不静默覆盖内建实现。
func (h *Host) Register(m *plugin.Manager) error {
	if err := h.Start(context.Background()); err != nil {
		return err
	}
	for _, lib := range h.manifest.Libraries {
		pid := sanitizeID("ext_" + h.manifest.ID + "_" + lib.Library)
		p := &plugin.Plugin{
			ID:      pid,
			Version: firstNonEmpty(h.manifest.Version, "0.0.0"),
			Type:    plugin.PluginCallable,
			Tools:   make([]plugin.Tool, 0, len(lib.Functions)),
		}
		for _, fn := range lib.Functions {
			symbol := lib.Library + "." + fn
			fid, _ := lib.FidelityFor(fn)
			p.Tools = append(p.Tools, plugin.Tool{
				Name:         fn,
				Desc:         fmt.Sprintf("out-of-process plugin %s (%s)", h.manifest.ID, fid),
				Capabilities: []string{symbol},
				Call:         h.callFor(symbol),
			})
		}
		if err := m.Register(p); err != nil {
			return fmt.Errorf("pluginhost: %s: register %s: %w", h.manifest.ID, lib.Library, err)
		}
		for _, fn := range lib.Functions {
			if err := m.Alias(lib.Library+"/"+fn, lib.Library+"."+fn); err != nil {
				return fmt.Errorf("pluginhost: %s: alias %s/%s: %w", h.manifest.ID, lib.Library, fn, err)
			}
		}
	}
	return nil
}

func (h *Host) callFor(symbol string) plugin.CallFunc {
	return func(ctx context.Context, args map[string]any) (any, error) {
		return h.Invoke(ctx, symbol, args)
	}
}

// Start 启动进程并完成握手。已启动则幂等。
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startLocked(ctx)
}

func (h *Host) startLocked(ctx context.Context) error {
	if h.closed {
		return fmt.Errorf("pluginhost: %s: host closed", h.manifest.ID)
	}
	if h.disabled != nil {
		return h.disabled
	}
	if h.proc != nil && h.proc.alive() {
		return nil
	}
	if h.proc != nil {
		h.recordRestart()
		h.proc = nil
	}
	if !h.allowRestart() {
		h.disabled = fmt.Errorf("pluginhost: %s: exceeded max_restarts=%d in 60s", h.manifest.ID, h.manifest.MaxRestarts)
		return h.disabled
	}

	prog, argv, err := h.manifest.ResolveCommand()
	if err != nil {
		h.disabled = fmt.Errorf("pluginhost: %s: %w", h.manifest.ID, err)
		return h.disabled
	}
	cmd := exec.Command(prog, argv...)
	cmd.Dir = h.manifest.WorkDir()
	cmd.Env = h.env()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	p := &process{
		cmd:     cmd,
		stdin:   stdin,
		dec:     json.NewDecoder(bufio.NewReaderSize(stdout, 1<<20)),
		pending: map[int64]chan rpcResponse{},
		done:    make(chan struct{}),
		stderr:  &tailBuffer{max: 8192},
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("pluginhost: %s: start: %w", h.manifest.ID, err)
	}
	go p.readLoop()
	go func() { _, _ = io.Copy(p.stderr, stderrPipe) }()
	h.proc = p

	deadline := time.Now().Add(h.opts.StartTimeout)
	initCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := h.initialize(initCtx); err != nil {
		p.kill()
		h.recordRestart()
		return fmt.Errorf("pluginhost: %s: handshake: %w%s", h.manifest.ID, err, p.stderrSuffix())
	}
	return nil
}

func (h *Host) initialize(ctx context.Context) error {
	p := h.proc
	params := map[string]any{
		"protocol":     ProtocolV1,
		"runtime":      map[string]any{"name": h.opts.RuntimeName, "version": h.opts.RuntimeVersion},
		"artifact_dir": h.opts.ArtifactDir,
		"symbols":      h.manifest.Symbols(),
	}
	raw, err := p.request(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var res struct {
		Protocol  string `json:"protocol"`
		ID        string `json:"id"`
		Version   string `json:"version"`
		Libraries []struct {
			Library   string   `json:"library"`
			Functions []string `json:"functions"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("initialize result: %w", err)
	}
	if res.Protocol != ProtocolV1 {
		return fmt.Errorf("protocol = %q, want %q", res.Protocol, ProtocolV1)
	}
	if res.ID != "" && res.ID != h.manifest.ID {
		return fmt.Errorf("plugin id = %q, manifest id = %q", res.ID, h.manifest.ID)
	}
	if h.manifest.Version != "" && res.Version != "" && res.Version != h.manifest.Version {
		return fmt.Errorf("plugin version = %q, manifest version = %q", res.Version, h.manifest.Version)
	}
	want := map[string]map[string]bool{}
	for _, lib := range h.manifest.Libraries {
		want[lib.Library] = map[string]bool{}
		for _, fn := range lib.Functions {
			want[lib.Library][fn] = true
		}
	}
	got := map[string]map[string]bool{}
	for _, lib := range res.Libraries {
		got[lib.Library] = map[string]bool{}
		for _, fn := range lib.Functions {
			got[lib.Library][fn] = true
		}
	}
	for lib, fns := range want {
		g, ok := got[lib]
		if !ok {
			return fmt.Errorf("handshake missing library %q", lib)
		}
		for fn := range fns {
			if !g[fn] {
				return fmt.Errorf("handshake missing %s.%s", lib, fn)
			}
		}
	}
	for lib, fns := range got {
		w, ok := want[lib]
		if !ok {
			return fmt.Errorf("handshake declares undeclared library %q", lib)
		}
		for fn := range fns {
			if !w[fn] {
				return fmt.Errorf("handshake declares undeclared %s.%s", lib, fn)
			}
		}
	}
	return nil
}

// Invoke 调用一个符号。ctx 的 deadline 会作为 deadline_ms 传给插件；
// 超时后宿主发 cancel，若插件在宽限期内不返回则杀进程（下次调用重启）。
func (h *Host) Invoke(ctx context.Context, symbol string, args map[string]any) (any, error) {
	if err := h.Start(ctx); err != nil {
		return nil, err
	}
	h.mu.Lock()
	p := h.proc
	h.mu.Unlock()
	if p == nil || !p.alive() {
		return nil, fmt.Errorf("pluginhost: %s: process not running", h.manifest.ID)
	}
	timeout := time.Duration(h.manifest.TimeoutMS) * time.Millisecond
	deadlineMS := timeout.Milliseconds()
	if dl, ok := ctx.Deadline(); ok {
		deadlineMS = time.Until(dl).Milliseconds()
		if deadlineMS <= 0 {
			return nil, context.DeadlineExceeded
		}
	}
	params := map[string]any{
		"symbol":      symbol,
		"args":        args,
		"deadline_ms": deadlineMS,
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(deadlineMS)*time.Millisecond+500*time.Millisecond)
	defer cancel()
	raw, err := p.requestWait(callCtx, "invoke", params)
	if err == nil {
		var out struct {
			Value any `json:"value"`
		}
		if uerr := json.Unmarshal(raw, &out); uerr != nil {
			return nil, fmt.Errorf("pluginhost: %s: %s: bad result: %w", h.manifest.ID, symbol, uerr)
		}
		return out.Value, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// 尽力通知；宽限期内没回应就杀掉，避免下一次调用踩到失步的流。
		_ = p.notify("cancel", map[string]any{"id": p.lastID()})
		grace, gcancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, _ = p.waitLast(grace)
		gcancel()
		p.kill()
		return nil, fmt.Errorf("pluginhost: %s: %s: %w", h.manifest.ID, symbol, err)
	}
	return nil, err
}

// Close 尝试优雅关闭，超时后强杀。
func (h *Host) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	if h.proc == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = h.proc.request(ctx, "shutdown", nil)
	h.proc.kill()
	h.proc = nil
	return nil
}

func (h *Host) allowRestart() bool {
	now := time.Now()
	keep := h.restarts[:0]
	for _, t := range h.restarts {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	h.restarts = keep
	return len(h.restarts) < maxRestarts(h.manifest.MaxRestarts)
}

func maxRestarts(n int) int {
	if n <= 0 {
		return DefaultMaxRestarts
	}
	return n
}

func (h *Host) recordRestart() { h.restarts = append(h.restarts, time.Now()) }

func (h *Host) env() []string {
	allow := []string{"PATH", "HOME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL", "SYSTEMROOT", "USERPROFILE", "PYTHONPATH"}
	out := make([]string, 0, len(allow)+len(h.manifest.Env)+3)
	for _, k := range allow {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	out = append(out, "NCODA_PLUGIN_PROTOCOL="+ProtocolV1, "NCODA_PLUGIN_ID="+h.manifest.ID)
	if h.opts.ArtifactDir != "" {
		out = append(out, "NCODA_ARTIFACT_DIR="+h.opts.ArtifactDir)
	}
	for k, v := range h.manifest.Env {
		out = append(out, k+"="+v)
	}
	return out
}

// ── 进程与协议 ─────────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	class := ""
	if e.Data != nil {
		if c, ok := e.Data["class"].(string); ok && c != "" {
			class = c + ": "
		}
	}
	return class + e.Message
}

type process struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	dec     *json.Decoder
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan rpcResponse
	nextID  atomic.Int64
	last    atomic.Int64
	done    chan struct{}
	failOne sync.Once
	err     error
	stderr  *tailBuffer
}

func (p *process) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *process) lastID() int64 { return p.last.Load() }

func (p *process) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := p.nextID.Add(1)
	p.last.Store(id)
	ch := make(chan rpcResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if err := p.send(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, p.failure()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

// requestWait 与 request 相同，但把 id 记在 last 上供 cancel 使用。
func (p *process) requestWait(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return p.request(ctx, method, params)
}

func (p *process) waitLast(ctx context.Context) (json.RawMessage, bool) {
	id := p.last.Load()
	p.mu.Lock()
	ch, ok := p.pending[id]
	p.mu.Unlock()
	if !ok {
		return nil, true
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, false
		}
		return resp.Result, true
	case <-ctx.Done():
		return nil, false
	case <-p.done:
		return nil, false
	}
}

func (p *process) notify(method string, params any) error {
	return p.send(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (p *process) send(req rpcRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(b)
	return err
}

func (p *process) readLoop() {
	for {
		var resp rpcResponse
		if err := p.dec.Decode(&resp); err != nil {
			p.fail(err)
			return
		}
		if resp.ID == nil {
			continue // notification（插件日志等）
		}
		p.mu.Lock()
		ch := p.pending[*resp.ID]
		p.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (p *process) fail(err error) {
	p.failOne.Do(func() {
		p.mu.Lock()
		if p.err == nil {
			p.err = err
		}
		close(p.done)
		p.pending = map[int64]chan rpcResponse{}
		p.mu.Unlock()
		_ = p.cmd.Wait()
	})
}

func (p *process) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err == nil {
		return fmt.Errorf("plugin process exited")
	}
	return fmt.Errorf("plugin process exited: %w", p.err)
}

func (p *process) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	// 同步标记死亡：否则下一次 Start 会看到 alive()=true，把请求写给
	// 一个已经关闭的 stdin（EOF），而不是触发重启。
	p.fail(errors.New("process killed"))
}

func (p *process) stderrSuffix() string {
	s := strings.TrimSpace(p.stderr.String())
	if s == "" {
		return ""
	}
	return " (stderr: " + s + ")"
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
