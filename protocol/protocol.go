package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"unsafe"

	"go.uber.org/zap"

	"robot/mem"
)

const maxBodyLength = 64 * 1024 * 1024

const (
	// magicNumber 用于校验消息是否采用本协议
	magicNumber uint32 = 0x7312
)

const (
	MessagePack uint16 = iota + 1
	Unknown
)

const (
	GetConfig uint16 = iota + 1 // 客户端请求服务端配置文件
	GetAction
)

type Header struct {
	Magic           uint32
	Version         uint32
	BodyLength      uint64
	ServerTimestamp uint64 // ms
	ContentType     uint16
	HandleID        uint16
}

type Message struct {
	*Header
	Body []byte
}

func NewMessage() *Message {
	header := &Header{
		Magic: magicNumber,
	}
	return &Message{
		Header: header,
	}
}

func (m *Message) SetVersion(version uint32) {
	m.Version = version
}

func (m *Message) SetServerTimestamp(timestamp uint64) {
	m.ServerTimestamp = timestamp
}

func (m *Message) SetContentType(contentTyp uint16) {
	m.ContentType = contentTyp
}

func (m *Message) SetHandleID(handleID uint16) {
	m.HandleID = handleID
}

func (m *Message) Encode() mem.Buffer {
	bodyLen := len(m.Body)
	m.BodyLength = uint64(bodyLen)

	totalLen := int(unsafe.Sizeof(Header{})) + bodyLen

	pool := mem.DefaultBufferPool()
	buf := pool.Get(totalLen)

	// 按小端序顺序写入 Header
	offset := 0
	binary.LittleEndian.PutUint32((*buf)[offset:], m.Magic) // offset 0-3
	offset += 4
	binary.LittleEndian.PutUint32((*buf)[offset:], m.Version) // offset 4-7
	offset += 4
	binary.LittleEndian.PutUint64((*buf)[offset:], m.BodyLength) // offset 8-15
	offset += 8
	binary.LittleEndian.PutUint64((*buf)[offset:], m.ServerTimestamp) // offset 16-23
	offset += 8
	binary.LittleEndian.PutUint16((*buf)[offset:], m.ContentType) // offset 24-25
	offset += 2
	binary.LittleEndian.PutUint16((*buf)[offset:], m.HandleID) // offset 26-27
	offset += 2

	// 写入 Body
	copy((*buf)[offset:], m.Body)

	return mem.NewBuffer(buf, pool)
}

func (m *Message) Decode(r io.Reader) error {
	defer func() {
		if err := recover(); err != nil {
			var errStack = make([]byte, 1024)
			n := runtime.Stack(errStack, true)
			zap.L().Error(fmt.Sprintf("panic in message decode: %v, stack: %s", err, errStack[:n]))
		}
	}()

	// 读取 Header 部分
	const serializedHeaderSize = 28
	headerBuf := make([]byte, serializedHeaderSize)
	n, err := io.ReadFull(r, headerBuf)
	if err != nil {
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("stream closed or insufficient data (%d/%d bytes): %w", n, serializedHeaderSize, err)
		}
		return fmt.Errorf("failed to read header (%d/%d bytes): %w", n, serializedHeaderSize, err)
	}

	// 逐个字段从小端序解码 Header
	offset := 0
	m.Magic = binary.LittleEndian.Uint32(headerBuf[offset:])
	offset += 4

	if m.Magic != magicNumber {
		// 是否是流关闭
		if m.Magic == 0 {
			return fmt.Errorf("stream appears to be closed (magic=0x0)")
		}
		return fmt.Errorf("invalid magic number: got 0x%x, expected 0x%x (raw bytes: %x)", m.Magic, magicNumber, headerBuf[:4])
	}

	m.Version = binary.LittleEndian.Uint32(headerBuf[offset:])
	offset += 4
	m.BodyLength = binary.LittleEndian.Uint64(headerBuf[offset:])
	offset += 8
	m.ServerTimestamp = binary.LittleEndian.Uint64(headerBuf[offset:])
	offset += 8
	m.ContentType = binary.LittleEndian.Uint16(headerBuf[offset:])
	offset += 2
	m.HandleID = binary.LittleEndian.Uint16(headerBuf[offset:])
	offset += 2

	if m.BodyLength > maxBodyLength {
		return fmt.Errorf("body length too large: %d bytes (max: %d)", m.BodyLength, maxBodyLength)
	}

	// 根据 Header 中的 BodyLength 读取 Body 部分
	if m.BodyLength > 0 {
		m.Body = make([]byte, m.BodyLength)
		n, err = io.ReadFull(r, m.Body)
		if err != nil {
			return fmt.Errorf("failed to read body (%d/%d bytes): %w", n, m.BodyLength, err)
		}
	} else {
		m.Body = nil
	}

	return nil
}
