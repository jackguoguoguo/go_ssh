package ui

import (
	"fmt"
	"strconv"
	"strings"

	"sshtool/internal/portfwd"
	"sshtool/internal/remotessh"
)

// handleForward 处理端口转发元命令：
//
//	forward L <listen> <target>   本地转发：在本机 listen 监听，经当前 SSH 会话隧道到 target
//	forward R <listen> <target>   远端转发：在 SSH 服务端 listen 监听，经隧道回连本机 target
//	forward stop <id>             停止指定转发（id 形如 L1 / R2）
//	forwards                    列出当前所有转发
//
// 转发需要当前活动标签是一个已连接的 SSH 会话（本地 shell 无法转发）。
func (m *Model) handleForward(rest []string) {
	if len(rest) == 0 {
		m.setMsg("用法：forward L|R <监听地址> <目标地址> | forward stop <id> | forwards")
		return
	}
	switch strings.ToLower(rest[0]) {
	case "stop":
		if len(rest) < 2 {
			m.setMsg("用法：forward stop <id> （id 形如 L1 / R2，用 forwards 查看）")
			return
		}
		if err := m.fwd.Stop(strings.ToUpper(rest[1])); err != nil {
			m.setMsg(err.Error())
			return
		}
		m.setMsg("已停止转发 " + strings.ToUpper(rest[1]))
		return
	case "forwards":
		fs := m.fwd.List()
		if len(fs) == 0 {
			m.setMsg("当前没有进行中的端口转发")
			return
		}
		var b strings.Builder
		for _, f := range fs {
			b.WriteString(fmt.Sprintf("  %s  %s -> %s\n", f.ID, f.Listen, f.Target))
		}
		m.setMsg("端口转发：\n" + strings.TrimRight(b.String(), "\n"))
		return
	case "l", "local":
		m.startForward(portfwd.KindLocal, rest[1:])
		return
	case "r", "remote":
		m.startForward(portfwd.KindRemote, rest[1:])
		return
	}
	m.setMsg("未知转发类型：请用 L（本地）或 R（远端）")
}

// startForward 解析监听/目标地址并启动一条转发。
func (m *Model) startForward(kind string, args []string) {
	if len(args) < 2 {
		m.setMsg("用法：forward " + kind + " <监听地址> <目标地址> （如 forward " + kind + " 8080 example.com:80）")
		return
	}
	listen := normalizeListen(args[0])
	target := args[1]

	s := m.activeSession()
	rs := sshSessionOf(s)
	if rs == nil {
		m.setMsg("端口转发需要当前标签为已连接的 SSH 会话")
		return
	}
	if rs.State() != remotessh.StateConnected {
		m.setMsg("端口转发需要当前 SSH 会话处于已连接状态")
		return
	}
	client := rs.Client()
	if client == nil {
		m.setMsg("当前会话尚无可用 SSH 客户端（可能正在重连）")
		return
	}

	var (
		f   *portfwd.Forward
		err error
	)
	if kind == portfwd.KindLocal {
		f, err = m.fwd.StartLocal(client, listen, target)
	} else {
		f, err = m.fwd.StartRemote(client, listen, target)
	}
	if err != nil {
		m.setMsg("启动转发失败：" + err.Error())
		return
	}
	m.setMsg(fmt.Sprintf("已启动%s转发 %s：%s -> %s", kindName(kind), f.ID, f.Listen, f.Target))
}

// normalizeListen 把监听地址补齐为 host:port 形式：纯端口补 localhost:，保留 0.0.0.0/空 host。
func normalizeListen(a string) string {
	if _, err := strconv.Atoi(a); err == nil {
		return "localhost:" + a
	}
	return a
}

func kindName(kind string) string {
	if kind == portfwd.KindLocal {
		return "本地"
	}
	return "远端"
}
