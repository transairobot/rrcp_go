package protocol

import (
	"bytes"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

func TestMessageEncodeDecode(t *testing.T) {
	// 创建消息
	msg := NewMessage()
	msg.SetVersion(1)
	msg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	msg.SetContentType(MessagePack)
	msg.SetFlag(Client)
	msg.Body = []byte("test message body")

	// 编码
	buf := msg.Encode()
	defer buf.Free()

	// 解码
	reader := bytes.NewReader(buf.ReadOnlyData())
	decodedMsg := NewMessage()
	err := decodedMsg.Decode(reader)

	// 验证
	if err != nil {
		t.Fatalf("解码消息失败: %v", err)
	}

	if decodedMsg.Magic != magicNumber {
		t.Errorf("魔数不匹配: 得到 %x, 期望 %x", decodedMsg.Magic, magicNumber)
	}

	if decodedMsg.Version != msg.Version {
		t.Errorf("版本不匹配: 得到 %d, 期望 %d", decodedMsg.Version, msg.Version)
	}

	if decodedMsg.ContentType != msg.ContentType {
		t.Errorf("内容类型不匹配: 得到 %d, 期望 %d", decodedMsg.ContentType, msg.ContentType)
	}

	if decodedMsg.Flag != msg.Flag {
		t.Errorf("标志不匹配: 得到 %d, 期望 %d", decodedMsg.Flag, msg.Flag)
	}

	if !bytes.Equal(decodedMsg.Body, msg.Body) {
		t.Errorf("消息体不匹配: 得到 %s, 期望 %s", string(decodedMsg.Body), string(msg.Body))
	}
}

func TestConfigSerialization(t *testing.T) {
	config := &Config{
		ServerToClientFrequency: 10,
		ClientToServerFrequency: 20,
	}

	// 序列化
	data, err := msgpack.Marshal(config)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}

	// 反序列化
	var decodedConfig Config
	err = msgpack.Unmarshal(data, &decodedConfig)
	if err != nil {
		t.Fatalf("反序列化配置失败: %v", err)
	}

	// 验证
	if decodedConfig.ServerToClientFrequency != config.ServerToClientFrequency {
		t.Errorf("服务器到客户端频率不匹配: 得到 %d, 期望 %d",
			decodedConfig.ServerToClientFrequency, config.ServerToClientFrequency)
	}

	if decodedConfig.ClientToServerFrequency != config.ClientToServerFrequency {
		t.Errorf("客户端到服务器频率不匹配: 得到 %d, 期望 %d",
			decodedConfig.ClientToServerFrequency, config.ClientToServerFrequency)
	}
}

func TestServerMessageSerialization(t *testing.T) {
	serverMsg := &ServerMessage{
		Timestamp: uint64(time.Now().UnixMilli()),
		Actions: []Action{
			{
				Type: "move",
				Parameters: map[string]interface{}{
					"x": 10.5,
					"y": 20.3,
				},
				Timestamp: uint64(time.Now().UnixMilli()),
			},
			{
				Type:       "stop",
				Parameters: map[string]interface{}{},
				Timestamp:  uint64(time.Now().UnixMilli()),
			},
		},
	}

	// 序列化
	data, err := msgpack.Marshal(serverMsg)
	if err != nil {
		t.Fatalf("序列化服务器消息失败: %v", err)
	}

	// 反序列化
	var decodedMsg ServerMessage
	err = msgpack.Unmarshal(data, &decodedMsg)
	if err != nil {
		t.Fatalf("反序列化服务器消息失败: %v", err)
	}

	// 验证
	if len(decodedMsg.Actions) != len(serverMsg.Actions) {
		t.Errorf("动作数量不匹配: 得到 %d, 期望 %d",
			len(decodedMsg.Actions), len(serverMsg.Actions))
	}

	for i, action := range decodedMsg.Actions {
		original := serverMsg.Actions[i]
		if action.Type != original.Type {
			t.Errorf("动作 %d 类型不匹配: 得到 %s, 期望 %s",
				i, action.Type, original.Type)
		}
	}
}

func BenchmarkMessageEncode(b *testing.B) {
	msg := NewMessage()
	msg.SetVersion(1)
	msg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	msg.SetContentType(MessagePack)
	msg.SetFlag(Client)
	msg.Body = make([]byte, 1024) // 1KB body

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := msg.Encode()
		buf.Free()
	}
}

func BenchmarkMessageDecode(b *testing.B) {
	msg := NewMessage()
	msg.SetVersion(1)
	msg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	msg.SetContentType(MessagePack)
	msg.SetFlag(Client)
	msg.Body = make([]byte, 1024) // 1KB body

	buf := msg.Encode()
	data := buf.ReadOnlyData()
	buf.Free()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader := bytes.NewReader(data)
		decodedMsg := NewMessage()
		decodedMsg.Decode(reader)
	}
}
