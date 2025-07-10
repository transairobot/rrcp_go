package protocol

// Config 表示从服务器发送到客户端的配置
type Config struct {
	// ServerToClientFrequency 表示服务器到客户端的每秒消息数
	ServerToClientFrequency uint32 `msgpack:"sc"`
	// ClientToServerFrequency 表示客户端到服务器的每秒消息数
	ClientToServerFrequency uint32 `msgpack:"cs"`
	// 必须包含name为main的Module
	Modules []WasmModule `msgpack:"modules"`
}

type WasmModule struct {
	Name string `msgpack:"name"`
	Wasm []byte `msgpack:"wasm"`
}

type SensorData struct {
	Servos []ServoStatus `msgpack:"servos,omitempty"`
	Images []Image       `msgpack:"images,omitempty"`
}

type Image struct {
	Width  uint32 `msgpack:"width"`
	Height uint32 `msgpack:"height"`
	Data   []byte `msgpack:"data"`
}

type ServoStatus struct {
	Angle float64 `msgpack:"angle"`
}

type Action struct {
	Timestamp uint64    `msgpack:"ts,omitempty"`
	Actions   []float64 `msgpack:"actions,omitempty"`
}

// ClientScript 表示客户端执行脚本配置
type ClientScript struct {
	Type     string                 `msgpack:"type"`             // "lua", "javascript", etc.
	Content  string                 `msgpack:"content"`          // 脚本内容
	Version  string                 `msgpack:"version"`          // 脚本版本
	Checksum string                 `msgpack:"checksum"`         // 脚本校验和
	Config   map[string]interface{} `msgpack:"config,omitempty"` // 脚本配置参数
}
