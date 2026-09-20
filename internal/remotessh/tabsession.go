package remotessh

import "sshtool/internal/store"

// TabID 会话 ID 的取值器：UI 把 SSH 会话与本地 shell 统一成「标签」抽象，
// 而 ID 是导出字段，无法再定义同名方法，故提供 TabID 供接口使用。
func (s *Session) TabID() string { return s.ID }

// ConnInfo 会话所属连接配置的取值器（同理：Conn 是字段，故另起方法名）。
func (s *Session) ConnInfo() store.Connection { return s.Conn }
