package test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/transairobot/rrcp_go/protocol"
)

var (
	addr = ":8000"
)

type Client struct {
	conn *quic.Conn
}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Connect() {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // 生产环境应验证证书
		NextProtos:         []string{"quic"},
	}

	conn, err := quic.DialAddr(context.Background(), addr, tlsConfig, &quic.Config{
		MaxIdleTimeout:  3 * time.Minute,
		KeepAlivePeriod: 20 * time.Second,
	})
	if err != nil {
		log.Printf("连接失败: %v", err)
		panic(err)
	}

	c.conn = conn
	localAddr := conn.LocalAddr().String()
	log.Printf("已连接到云平台: %s", addr)
	log.Printf("客户端本地地址: %s", localAddr)

	sendHTTP(localAddr)

	log.Printf("发送地址成功")

	res := c.GetConfig()
	fmt.Println(res)

	// 启动监听云平台的主动请求
	go c.listenForCloudRequests()
}

func (c *Client) GetConfig() map[string]any {
	// 创建消息
	msg := protocol.NewMessage()
	msg.SetVersion(1)
	msg.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	msg.SetContentType(protocol.MessagePack)
	msg.SetHandleID(protocol.GetConfig)
	msg.Body = []byte{}

	buf := msg.Encode()
	defer buf.Free()

	// 主动打开流
	stream, err := c.conn.OpenStreamSync(context.Background())
	if err != nil {
		fmt.Printf("failed to open stream: %v", err)
	}
	defer stream.Close()

	if err := stream.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		fmt.Printf("failed to set write deadline: %v", err)
	}

	if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
		fmt.Printf("failed to send config request: %v", err)
	}

	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		fmt.Printf("failed to set read deadline: %v", err)
	}

	response := protocol.NewMessage()
	if err := response.Decode(stream); err != nil {
		fmt.Printf("failed to decode response: %v", err)
	}

	result := make(map[string]any)
	if len(response.Body) > 0 {
		if err := msgpack.Unmarshal(response.Body, &result); err != nil {
			fmt.Printf("failed to unmarshal response: %v", err)
		}
	}

	return result
}

func (c *Client) listenForCloudRequests() {
	for {
		stream, err := c.conn.AcceptStream(context.Background())
		if err != nil {
			log.Printf("接受流失败: %v", err)
			return
		}

		go c.handleCloudRequest(stream)
	}
}

// handleCloudRequest 处理云平台的主动请求
func (c *Client) handleCloudRequest(stream *quic.Stream) {
	defer stream.Close()

	// 读取请求
	msg := protocol.NewMessage()
	if err := msg.Decode(stream); err != nil {
		log.Printf("解码消息失败: %v", err)
		return
	}

	log.Printf("收到云平台请求: handler_id=%d", msg.HandleID)

	// 反序列化请求
	var req interface{}
	if len(msg.Body) > 0 {
		if err := json.Unmarshal(msg.Body, &req); err != nil {
			log.Printf("反序列化请求失败: %v", err)
			return
		}
	}

	result := map[string]any{
		"success": true,
	}

	// 发送响应
	c.sendResponse(stream, msg.HandleID, result)
}

// sendResponse 发送响应
func (c *Client) sendResponse(stream *quic.Stream, handlerID uint16, result interface{}) {
	var responseData []byte
	if result != nil {
		var err error
		responseData, err = json.Marshal(result)
		if err != nil {
			log.Printf("序列化响应失败: %v", err)
			return
		}
	}

	response := protocol.NewMessage()
	response.SetVersion(1)
	response.SetServerTimestamp(uint64(time.Now().UnixMilli()))
	response.SetContentType(protocol.Json)
	response.SetHandleID(handlerID)
	response.Body = responseData

	buf := response.Encode()
	defer buf.Free()

	if _, err := stream.Write(buf.ReadOnlyData()); err != nil {
		log.Printf("发送响应失败: %v", err)
	}
}

// fixIPv6Address 修复IPv6地址格式问题
func fixIPv6Address(addr string) string {
	// 将 [::]:port 转换为 [::1]:port (loopback)
	if strings.Contains(addr, "[::]:") {
		return strings.Replace(addr, "[::]:", "[::1]:", 1)
	}
	return addr
}

func sendHTTP(addr string) {
	log.Printf("原始客户端地址: %s", addr)

	fixedAddr := fixIPv6Address(addr)
	if fixedAddr != addr {
		log.Printf("修复IPv6地址: %s -> %s", addr, fixedAddr)
	}

	cli := http.DefaultClient
	req := map[string]any{
		"addr": fixedAddr,
	}

	data, _ := json.Marshal(req)
	log.Printf("发送HTTP请求，地址: %s", fixedAddr)

	resp, err := cli.Post("http://localhost:8080/test", "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("HTTP请求失败: %v", err)
	} else {
		log.Printf("HTTP请求成功，状态码: %d", resp.StatusCode)
		resp.Body.Close()
	}
}

func TestClient(t *testing.T) {
	c := NewClient()
	c.Connect()

	time.Sleep(time.Minute * 2)
}
