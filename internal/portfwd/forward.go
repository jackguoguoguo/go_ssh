// Package portfwd 提供 SSH 端口转发：本地转发（-L）与远端转发（-R）。
//
// 本地转发：在本机监听，连接经 SSH 通道发往目标地址（常用于访问内网服务）。
// 远端转发：在 SSH 服务端监听，连接经 SSH 通道回到本机目标（常用于把本机服务暴露给远端）。
//
// 转发参数由调用方（命令行元命令）显式给出，不写入 store 的连接配置，
// 以避免触碰 store 的冻结契约。
package portfwd

import (
	"fmt"
	"io"
	"net"
	"sync"

	sshx "golang.org/x/crypto/ssh"
)

// Kind 转发方向。
const (
	KindLocal  = "L" // 本地转发：本机监听 → 远端目标
	KindRemote = "R" // 远端转发：服务端监听 → 本机目标
)

// Forward 描述一条转发规则。
type Forward struct {
	ID     string // 形如 "L1" / "R2"
	Kind   string
	Listen string // 监听地址
	Target string // 目标地址
}

// entry 是内部的一条转发及其关闭句柄。
type entry struct {
	Forward
	ln     net.Listener
	closed bool
}

// Manager 管理当前进程内的所有转发。
type Manager struct {
	mu  sync.Mutex
	seq int
	fw  map[string]*entry
}

// New 创建一个转发管理器。
func New() *Manager {
	return &Manager{fw: map[string]*entry{}}
}

// StartLocal 启动本地转发：在本机 listen 监听，经 client 转发到 target。
func (m *Manager) StartLocal(client *sshx.Client, listen, target string) (*Forward, error) {
	if client == nil {
		return nil, fmt.Errorf("会话尚未建立 SSH 连接")
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("监听 %s 失败: %w", listen, err)
	}
	e := m.add(KindLocal, listen, target, ln)
	go m.serveLocal(e, client, target)
	return &e.Forward, nil
}

// StartRemote 启动远端转发：在 SSH 服务端 listen 监听，经 client 回连本机 target。
func (m *Manager) StartRemote(client *sshx.Client, listen, target string) (*Forward, error) {
	if client == nil {
		return nil, fmt.Errorf("会话尚未建立 SSH 连接")
	}
	ln, err := client.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("请求远端监听 %s 失败: %w", listen, err)
	}
	e := m.add(KindRemote, listen, target, ln)
	go m.serveRemote(e, target)
	return &e.Forward, nil
}

func (m *Manager) add(kind, listen, target string, ln net.Listener) *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	f := Forward{
		ID:     fmt.Sprintf("%s%d", kind, m.seq),
		Kind:   kind,
		Listen: ln.Addr().String(), // 记录真实监听地址（listen 可能是 :0，实际端口由系统分配）
		Target: target,
	}
	e := &entry{Forward: f, ln: ln}
	m.fw[f.ID] = e
	return e
}

// serveLocal 接受本机连接并桥接到远端目标。
func (m *Manager) serveLocal(e *entry, client *sshx.Client, target string) {
	for {
		lc, err := e.ln.Accept()
		if err != nil {
			return // 监听关闭即退出
		}
		go func(lc net.Conn) {
			defer lc.Close()
			rc, err := client.Dial("tcp", target)
			if err != nil {
				return
			}
			defer rc.Close()
			bridge(lc, rc)
		}(lc)
	}
}

// serveRemote 接受服务端发起的连接并桥接到本机目标。
func (m *Manager) serveRemote(e *entry, target string) {
	for {
		rc, err := e.ln.Accept()
		if err != nil {
			return
		}
		go func(rc net.Conn) {
			defer rc.Close()
			lc, err := net.Dial("tcp", target)
			if err != nil {
				return
			}
			defer lc.Close()
			bridge(rc, lc)
		}(rc)
	}
}

// bridge 双向拷贝，任一端结束即关闭两侧。
func bridge(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	<-done
}

// Stop 停止并移除一条转发。
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	e, ok := m.fw[id]
	if ok {
		delete(m.fw, id)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("转发 %s 不存在", id)
	}
	e.closed = true
	return e.ln.Close()
}

// List 返回当前所有转发（按 ID 排序）。
func (m *Manager) List() []Forward {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Forward, 0, len(m.fw))
	for _, e := range m.fw {
		out = append(out, e.Forward)
	}
	sortByID(out)
	return out
}

// StopAll 停止全部转发。
func (m *Manager) StopAll() {
	for _, f := range m.List() {
		_ = m.Stop(f.ID)
	}
}

// sortByID 按 ID 稳定排序（L1/L2.../R1...）。
func sortByID(fs []Forward) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && fs[j].ID < fs[j-1].ID; j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}
