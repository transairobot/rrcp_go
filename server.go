package robot

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/vmihailenco/msgpack/v5"
	"go.uber.org/zap"

	"robot/protocol"
)

// Server 表示机器人控制服务器
type Server struct {
	config  *protocol.Config
	lis     *quic.Listener
	clients sync.Map // map[*quic.Connection]*ClientSession
	logger  *zap.Logger
	ctx     context.Context
	cancel  context.CancelFunc
	stats   *ServerStats

	// 消息处理器
	clientDataHandler func(*protocol.ClientMessage, string) // clientMessage, clientAddr

	// 动作管理
	pendingActions sync.Map // map[uint64]*PendingAction 用于跟踪已发送的动作
	actionID       atomic.Uint64

	// 数据附加机制
	attachedData   map[string]interface{} // 要附加到下一条消息的数据
	attachedDataMu sync.RWMutex           // 保护attachedData
}

// ClientSession 表示已连接的客户端会话
type ClientSession struct {
	RemoteAddr   string    `json:"remote_addr"`
	ConnectedAt  time.Time `json:"connected_at"`
	LastActivity time.Time `json:"last_activity"`
	MessagesSent uint64    `json:"messages_sent"`
	MessagesRecv uint64    `json:"messages_recv"`
	ErrorCount   uint64    `json:"error_count"`
	ConfigSent   bool      `json:"config_sent"`

	conn *quic.Conn
	mu   sync.RWMutex
}

// ServerStats 跟踪服务器性能指标
type ServerStats struct {
	StartTime        time.Time     `json:"start_time"`
	TotalConnections atomic.Uint64 `json:"total_connections"`
	ActiveClients    atomic.Uint64 `json:"active_clients"`
	MessagesSent     atomic.Uint64 `json:"messages_sent"`
	MessagesReceived atomic.Uint64 `json:"messages_received"`
	ActionsSent      atomic.Uint64 `json:"actions_sent"` // 动作发送统计
	Errors           atomic.Uint64 `json:"errors"`
}

// NewServer 创建一个新的机器人控制服务器
func NewServer(config *protocol.Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		config:       config,
		logger:       zap.L(),
		ctx:          ctx,
		cancel:       cancel,
		stats:        &ServerStats{StartTime: time.Now()},
		attachedData: make(map[string]interface{}),
	}
}

// SetClientDataHandler 设置客户端数据消息的处理器
func (s *Server) SetClientDataHandler(handler func(*protocol.ClientMessage, string)) {
	s.clientDataHandler = handler
}

// Serve 启动服务器并接受连接
func (s *Server) Serve(addr string) error {
	cert, err := loadCert("cert.pem", "private.pem")
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"quic"},
	}

	listener, err := quic.ListenAddr(addr, tlsConfig, &quic.Config{
		MaxIdleTimeout:                 3 * time.Minute,
		KeepAlivePeriod:                20 * time.Second,
		MaxIncomingStreams:             2000,
		MaxIncomingUniStreams:          2000,
		EnableDatagrams:                false,
		InitialStreamReceiveWindow:     512 * 1024,
		MaxStreamReceiveWindow:         1024 * 1024,
		InitialConnectionReceiveWindow: 1024 * 1024,
		MaxConnectionReceiveWindow:     2048 * 1024,
	})
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	s.lis = listener
	s.logger.Info("服务器已启动", zap.String("addr", addr))

	for {
		conn, err := listener.Accept(s.ctx)
		if err != nil {
			select {
			case <-s.ctx.Done():
				return nil
			default:
				s.logger.Error("接受连接失败", zap.Error(err))
				continue
			}
		}

		go s.handleConnection(conn)
	}
}

// handleConnection 处理单个连接
func (s *Server) handleConnection(conn *quic.Conn) {
	defer conn.CloseWithError(0, "会话结束")

	session := &ClientSession{
		RemoteAddr:  conn.RemoteAddr().String(),
		ConnectedAt: time.Now(),
		conn:        conn,
	}

	s.clients.Store(conn, session)
	defer s.clients.Delete(conn)
	s.stats.TotalConnections.Add(1)

	s.logger.Info("客户端已连接", zap.String("remote", conn.RemoteAddr().String()))

	for {
		stream, err := conn.AcceptStream(s.ctx)
		if err != nil {
			s.logger.Debug("接受流失败", zap.Error(err))
			return
		}

		go s.handleStream(session, stream)
	}
}

// handleStream 处理单个流
func (s *Server) handleStream(session *ClientSession, stream *quic.Stream) {
	defer stream.Close()

	msg := protocol.NewMessage()
	if err := msg.Decode(stream); err != nil {
		s.logger.Debug("解码消息失败", zap.Error(err))
		return
	}

	session.mu.Lock()
	session.LastActivity = time.Now()
	session.mu.Unlock()

	switch msg.Flag {
	case protocol.GetReq:
		s.handleGetRequest(session, stream, msg)
	case protocol.Client:
		s.handleClientData(session, msg)
	default:
		s.logger.Warn("未知消息标志", zap.Uint16("flag", msg.Flag))
	}
}

// handleGetRequest 处理配置请求
func (s *Server) handleGetRequest(session *ClientSession, stream *quic.Stream, msg *protocol.Message) {
	if session.ConfigSent {
		s.logger.Warn("配置已经发送给客户端")
		return
	}

	configData, err := msgpack.Marshal(s.config)
	if err != nil {
		s.logger.Error("序列化配置失败", zap.Error(err))
		return
	}

	response := protocol.NewMessage()
	response.SetVersion(1)
	response.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	response.SetContentType(protocol.MessagePack)
	response.SetFlag(protocol.GetRes)
	response.Body = configData

	buf := response.Encode()
	defer buf.Free()

	if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
		s.logger.Error("发送配置失败", zap.Error(err))
		return
	}

	session.mu.Lock()
	session.ConfigSent = true
	session.mu.Unlock()

	s.logger.Info("配置已发送给客户端")
}

// handleClientData 处理数据请求
func (s *Server) handleClientData(session *ClientSession, msg *protocol.Message) {
	if msg.ContentType != protocol.MessagePack {
		s.logger.Warn("不支持的内容类型", zap.Uint16("type", msg.ContentType))
		return
	}

	var clientMsg protocol.ClientMessage
	if err := msgpack.Unmarshal(msg.Body, &clientMsg); err != nil {
		s.logger.Error("反序列化客户端消息失败", zap.Error(err))
		s.stats.Errors.Add(1)
		return
	}

	s.stats.MessagesReceived.Add(1)
	clientAddr := session.conn.RemoteAddr().String()

	s.logger.Debug("收到客户端数据", zap.Any("msg", clientMsg))
	s.logger.Debug("收到客户端数据",
		zap.Uint64("timestamp", clientMsg.Timestamp),
		zap.Bool("has_sensor", clientMsg.SensorData != nil),
		zap.Bool("has_image", clientMsg.ImageData != nil),
		zap.Bool("has_ack", clientMsg.Acknowledgment != nil))

	// 调用用户提供的处理器
	if s.clientDataHandler != nil {
		go s.clientDataHandler(&clientMsg, clientAddr)
	}
}

// SendMessage 向特定客户端发送消息
func (s *Server) SendMessage(clientAddr string, msg *protocol.ServerMessage) error {
	var targetSession *ClientSession
	var targetConn *quic.Conn

	s.clients.Range(func(key, value interface{}) bool {
		conn := key.(*quic.Conn)
		if conn.RemoteAddr().String() == clientAddr {
			targetConn = conn
			targetSession = value.(*ClientSession)
			return false
		}
		return true
	})

	if targetSession == nil {
		return fmt.Errorf("未找到客户端: %s", clientAddr)
	}

	return s.sendMessage(targetConn, msg)
}

// SendAction 向特定客户端发送动作
func (s *Server) SendAction(clientAddr string, actionType string, parameters map[string]interface{}, priority int, requireAck bool, timeout int) error {
	actionID := s.actionID.Add(1)

	action := protocol.Action{
		ID:          actionID,
		Type:        actionType,
		Parameters:  parameters,
		Timestamp:   uint64(time.Now().UnixMilli()),
		Priority:    priority,
		Timeout:     timeout,
		RequireAck:  requireAck,
		Cancellable: true,
	}

	serverMsg := &protocol.ServerMessage{
		Timestamp: uint64(time.Now().UnixMilli()),
		MessageID: s.actionID.Add(1),
		Actions:   []protocol.Action{action},
	}

	return s.SendMessage(clientAddr, serverMsg)
}

// BroadcastMessage 向所有已连接的客户端发送消息
func (s *Server) BroadcastMessage(msg *protocol.ServerMessage) {
	s.clients.Range(func(key, value interface{}) bool {
		conn := key.(*quic.Conn)
		go func(c *quic.Conn) {
			if err := s.sendMessage(c, msg); err != nil {
				s.logger.Debug("广播消息发送失败",
					zap.String("client", c.RemoteAddr().String()),
					zap.Error(err))
			}
		}(conn)
		return true
	})
}

// sendMessage 发送消息
func (s *Server) sendMessage(conn *quic.Conn, msg *protocol.ServerMessage) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("打开流失败: %w", err)
	}
	defer stream.Close()

	// 获取并清除此消息的附加数据
	s.attachedDataMu.Lock()
	var attachedData map[string]interface{}
	if len(s.attachedData) > 0 {
		attachedData = make(map[string]interface{})
		for k, v := range s.attachedData {
			attachedData[k] = v
		}
		s.attachedData = make(map[string]interface{}) // 复制后清除
	}
	s.attachedDataMu.Unlock()

	// 在服务器消息中包含附加数据
	if attachedData != nil {
		if msg.AttachedData == nil {
			msg.AttachedData = attachedData
		} else {
			// 与现有附加数据合并
			for k, v := range attachedData {
				msg.AttachedData[k] = v
			}
		}
	}

	data, err := msgpack.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化服务器消息失败: %w", err)
	}

	protocolMsg := protocol.NewMessage()
	protocolMsg.SetVersion(1)
	protocolMsg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	protocolMsg.SetContentType(protocol.MessagePack)
	protocolMsg.SetFlag(protocol.Server)
	protocolMsg.Body = data

	buf := protocolMsg.Encode()
	defer buf.Free()

	if err := stream.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("设置写入截止时间失败: %w", err)
	}

	if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
		return fmt.Errorf("写入消息失败: %w", err)
	}

	s.stats.MessagesSent.Add(1)
	return nil
}

// GetStats 返回当前服务器统计信息
func (s *Server) GetStats() *ServerStats {
	var activeClients uint64
	s.clients.Range(func(key, value interface{}) bool {
		activeClients++
		return true
	})
	s.stats.ActiveClients.Store(activeClients)
	return s.stats
}

// GetConnectedClients 返回已连接客户端地址列表
func (s *Server) GetConnectedClients() []string {
	var clients []string
	s.clients.Range(func(key, value interface{}) bool {
		conn := key.(*quic.Conn)
		clients = append(clients, conn.RemoteAddr().String())
		return true
	})
	return clients
}

// Stop 停止服务器
func (s *Server) Stop() error {
	s.cancel()
	if s.lis != nil {
		return s.lis.Close()
	}
	return nil
}

// AttachData 附加自定义数据，将与下一条服务器消息一起发送
func (s *Server) AttachData(key string, value interface{}) {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	s.attachedData[key] = value
}

// AttachDataBatch 附加多个键值对，将与下一条服务器消息一起发送
func (s *Server) AttachDataBatch(data map[string]interface{}) {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	for k, v := range data {
		s.attachedData[k] = v
	}
}

// GetAttachedData 返回当前附加数据的副本
func (s *Server) GetAttachedData() map[string]interface{} {
	s.attachedDataMu.RLock()
	defer s.attachedDataMu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range s.attachedData {
		result[k] = v
	}
	return result
}

// ClearAttachedData 移除所有附加数据
func (s *Server) ClearAttachedData() {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	s.attachedData = make(map[string]interface{})
}
