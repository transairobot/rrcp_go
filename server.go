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
	clientDataHandler func(*protocol.SensorData, string) any // clientMessage, clientAddr

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
func (s *Server) SetClientDataHandler(handler func(*protocol.SensorData, string) any) {
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
	case protocol.GetConfig:
		s.handleGetRequest(session, stream, msg)
	case protocol.GetAction:
		s.handleClientData(session, stream, msg)
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
	response.SetFlag(protocol.GetConfig)
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
func (s *Server) handleClientData(session *ClientSession, stream *quic.Stream, msg *protocol.Message) {
	if msg.ContentType != protocol.MessagePack {
		s.logger.Warn("不支持的内容类型", zap.Uint16("type", msg.ContentType))
		return
	}

	var clientMsg protocol.SensorData
	if err := msgpack.Unmarshal(msg.Body, &clientMsg); err != nil {
		s.logger.Error("反序列化客户端消息失败", zap.Error(err))
		s.stats.Errors.Add(1)
		return
	}

	s.stats.MessagesReceived.Add(1)
	clientAddr := session.conn.RemoteAddr().String()

	s.logger.Debug("收到客户端数据", zap.Any("sensors", clientMsg.Servos), zap.Any("images", clientMsg.Images))

	f := func(data any) {
		res, err := msgpack.Marshal(data)
		if err != nil {
			s.logger.Error("序列化客户端数据失败", zap.Error(err))
			return
		}

		response := protocol.NewMessage()
		response.SetVersion(1)
		response.SetServerTimestamp(uint64(time.Now().UnixMilli()))
		response.SetContentType(protocol.MessagePack)
		response.SetFlag(protocol.GetAction)
		response.Body = res

		buf := response.Encode()
		defer buf.Free()

		if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
			s.logger.Error("发送响应失败", zap.Error(err))
			return
		}
	}

	// 调用用户提供的处理器
	var data any
	if s.clientDataHandler != nil {
		data = s.clientDataHandler(&clientMsg, clientAddr)
	}
	f(data)
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
