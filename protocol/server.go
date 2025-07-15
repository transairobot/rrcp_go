package protocol

// Heartbeat 用于连接健康监控
type Heartbeat struct {
	ServerTime     uint64 `msgpack:"server_time"`
	SequenceNumber uint64 `msgpack:"sequence"`
	ExpectedReply  bool   `msgpack:"expected_reply"`
}
