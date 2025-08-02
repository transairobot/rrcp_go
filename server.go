package robot

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/vmihailenco/msgpack/v5"
	"go.uber.org/zap"

	"github.com/transairobot/rrcp_go/protocol"
)

type Server struct {
	conf    *Config
	svcConf *protocol.ServiceConfig
	lis     *quic.Listener
	clients sync.Map // map[*quic.Connection]*ClientSession
	logger  *zap.Logger
	ctx     context.Context
	cancel  context.CancelFunc

	// 消息处理器
	clientDataHandler func(*protocol.SensorData, string) any

	// 数据附加机制
	attachedData   map[string]any // 要附加到下一条消息的数据
	attachedDataMu sync.RWMutex   // 保护attachedData
}

type Config struct {
	CertFile    string
	PrivateFile string
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

// NewServer 创建一个新的机器人控制服务器
func NewServer(svc *protocol.ServiceConfig, conf *Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		svcConf:      svc,
		conf:         conf,
		logger:       zap.L(),
		ctx:          ctx,
		cancel:       cancel,
		attachedData: make(map[string]any),
	}
}

// SetClientDataHandler 设置客户端数据消息的处理器
func (s *Server) SetClientDataHandler(handler func(*protocol.SensorData, string) any) {
	s.clientDataHandler = handler
}

// RequestConnection 向指定机器人发起请求（通过已建立的连接）
func (s *Server) RequestConnection(ctx context.Context, robotAddr string, handlerID uint16, req any) (map[string]any, error) {
	// 查找机器人连接
	var targetSession *ClientSession
	s.clients.Range(func(key, value any) bool {
		session := value.(*ClientSession)
		if session.RemoteAddr == robotAddr {
			targetSession = session
			return false
		}
		return true
	})

	if targetSession == nil {
		return nil, fmt.Errorf("robot not connected: %s", robotAddr)
	}

	return s.requestToSession(ctx, targetSession, handlerID, req)
}

// requestToSession 向指定会话发起请求
func (s *Server) requestToSession(ctx context.Context, session *ClientSession, handlerID uint16, req any) (map[string]any, error) {
	// 序列化请求数据
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建消息
	msg := protocol.NewMessage()
	msg.SetVersion(1)
	msg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	msg.SetContentType(protocol.Json)
	msg.SetHandleID(handlerID)
	msg.Body = reqData

	// 主动打开流
	stream, err := session.conn.OpenStreamSync(s.ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	buf := msg.Encode()
	defer buf.Free()

	if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	response := protocol.NewMessage()
	if err := response.Decode(stream); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	result := make(map[string]any)
	if len(response.Body) > 0 {
		if err := json.Unmarshal(response.Body, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	s.logger.Debug("请求完成", zap.String("robot", session.RemoteAddr), zap.Uint16("handler_id", handlerID))

	return result, nil
}

// Serve 启动服务器并接受连接
func (s *Server) Serve(addr string) error {
	cert, err := loadCert(s.conf.CertFile, s.conf.PrivateFile)
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

	switch msg.HandleID {
	case protocol.GetConfig:
		s.handleGetRequest(session, stream, msg)
	case protocol.GetAction:
		s.handleClientData(session, stream, msg)
	default:
		s.logger.Warn("未知消息标志", zap.Uint16("flag", msg.HandleID))
	}
}

// handleGetRequest 处理配置请求
func (s *Server) handleGetRequest(session *ClientSession, stream *quic.Stream, msg *protocol.Message) {
	if session.ConfigSent {
		s.logger.Warn("配置已经发送给客户端")
		return
	}

	configData, err := msgpack.Marshal(s.svcConf)
	if err != nil {
		s.logger.Error("序列化配置失败", zap.Error(err))
		return
	}

	response := protocol.NewMessage()
	response.SetVersion(1)
	response.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	response.SetContentType(protocol.MessagePack)
	response.SetHandleID(protocol.GetConfig)
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
		return
	}

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
		response.SetHandleID(protocol.GetAction)
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

// Stop 停止服务器
func (s *Server) Stop() error {
	s.cancel()

	if s.lis != nil {
		return s.lis.Close()
	}
	return nil
}

// AttachData 附加自定义数据，将与下一条服务器消息一起发送
func (s *Server) AttachData(key string, value any) {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	s.attachedData[key] = value
}

// AttachDataBatch 附加多个键值对，将与下一条服务器消息一起发送
func (s *Server) AttachDataBatch(data map[string]any) {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	for k, v := range data {
		s.attachedData[k] = v
	}
}

// GetAttachedData 返回当前附加数据的副本
func (s *Server) GetAttachedData() map[string]any {
	s.attachedDataMu.RLock()
	defer s.attachedDataMu.RUnlock()

	result := make(map[string]any)
	for k, v := range s.attachedData {
		result[k] = v
	}
	return result
}

// ClearAttachedData 移除所有附加数据
func (s *Server) ClearAttachedData() {
	s.attachedDataMu.Lock()
	defer s.attachedDataMu.Unlock()
	s.attachedData = make(map[string]any)
}
