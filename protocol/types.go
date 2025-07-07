package protocol

// Config 表示从服务器发送到客户端的配置
type Config struct {
	// ServerToClientFrequency 表示服务器到客户端的每秒消息数
	ServerToClientFrequency uint32 `msgpack:"sc"`
	// ClientToServerFrequency 表示客户端到服务器的每秒消息数
	ClientToServerFrequency uint32 `msgpack:"cs"`
}

type SensorData struct {
	Servos     []ServoStatus       `msgpack:"servos,omitempty"`     // 舵机状态
	Ultrasonic []UltrasonicReading `msgpack:"ultrasonic,omitempty"` // 超声波传感器读数数组
}

type UltrasonicReading struct {
	ID       int     `msgpack:"id"`
	Distance float64 `msgpack:"distance"` // 厘米
	Angle    float64 `msgpack:"angle"`    // 传感器方向
}

type ServoStatus struct {
	// TODO
}

// ImageData 表示来自客户端的图像数据
type ImageData struct {
	Width  int    `msgpack:"width"`
	Height int    `msgpack:"height"`
	Data   []byte `msgpack:"data"`
}

// ActionAck 表示对接收到的动作的确认
type ActionAck struct {
	ActionID  uint64      `msgpack:"action_id"`
	Status    string      `msgpack:"status"` // "received", "executing", "completed", "failed"
	Error     string      `msgpack:"error,omitempty"`
	Result    interface{} `msgpack:"result,omitempty"`
	Timestamp uint64      `msgpack:"timestamp"`
}

// Action 表示从服务器到客户端的动作命令
type Action struct {
	ID          uint64                 `msgpack:"id"`
	Type        string                 `msgpack:"type"`
	Parameters  map[string]interface{} `msgpack:"parameters"`
	Timestamp   uint64                 `msgpack:"timestamp"`
	Priority    int                    `msgpack:"priority"`            // 0=低, 1=正常, 2=高, 3=关键
	Timeout     int                    `msgpack:"timeout"`             // 超时时间（毫秒）
	RequireAck  bool                   `msgpack:"require_ack"`         // 是否需要确认
	Cancellable bool                   `msgpack:"cancellable"`         // 是否可以取消
	ParentID    uint64                 `msgpack:"parent_id,omitempty"` // 父动作ID（用于动作链）
}

// ClientScript 表示客户端执行脚本配置
type ClientScript struct {
	Type     string                 `msgpack:"type"`             // "lua", "javascript", etc.
	Content  string                 `msgpack:"content"`          // 脚本内容
	Version  string                 `msgpack:"version"`          // 脚本版本
	Checksum string                 `msgpack:"checksum"`         // 脚本校验和
	Config   map[string]interface{} `msgpack:"config,omitempty"` // 脚本配置参数
}
