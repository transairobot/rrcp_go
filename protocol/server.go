package protocol

// ServerMessage 表示从服务端发送到客户端的数据
type ServerMessage struct {
	Timestamp    uint64                 `msgpack:"timestamp"`
	MessageID    uint64                 `msgpack:"message_id"`
	Actions      []Action               `msgpack:"actions,omitempty"`
	Heartbeat    *Heartbeat             `msgpack:"heartbeat,omitempty"`
	AttachedData map[string]interface{} `msgpack:"attached_data,omitempty"` // 开发者附加的自定义数据
}

// Heartbeat 用于连接健康监控
type Heartbeat struct {
	ServerTime     uint64 `msgpack:"server_time"`
	SequenceNumber uint64 `msgpack:"sequence"`
	ExpectedReply  bool   `msgpack:"expected_reply"`
}
