package test

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	robot "github.com/transairobot/rrcp_go"
	"github.com/transairobot/rrcp_go/protocol"
)

type Server struct {
	srv *robot.Server
}

func NewServer() *Server {
	mainData, err := os.ReadFile("/home/frank/projects/go/robot-protocol/main.wasm")
	if err != nil {
		panic(err)
	}

	config := &protocol.ServiceConfig{
		ServerToClientFrequency: 10, // 10 Hz
		ClientToServerFrequency: 20, // 20 Hz
		Modules: []protocol.WasmModule{
			{
				Name: "main",
				Wasm: mainData,
			},
		},
	}
	srv := robot.NewServer(config, &robot.Config{
		CertFile:    "/home/frank/projects/go/robot-protocol/cert.pem",
		PrivateFile: "/home/frank/projects/go/robot-protocol/private.pem",
	})

	return &Server{srv: srv}
}

var clientAddr string

func TestServer(t *testing.T) {
	go startHTTP()

	srv := NewServer()
	go func() {
		log.Printf("服务器启动在地址: %s", addr)
		if err := srv.srv.Serve(addr); err != nil {
			log.Printf("服务器启动失败: %v", err)
		}
	}()

	time.Sleep(time.Second * 5)

	res, err := srv.srv.RequestConnection(context.Background(), clientAddr, protocol.InstallReq, map[string]any{
		"command": 1,
		"config":  "config",
	})
	if err != nil {
		fmt.Printf("请求连接失败: %v\n", err)
		return
	}
	fmt.Printf("连接请求响应: %v\n", res)
}

func startHTTP() {
	engine := gin.Default()
	engine.Handle(http.MethodPost, "test", func(c *gin.Context) {
		var req map[string]string
		if err := c.ShouldBind(&req); err != nil {
			fmt.Println(err)
		}
		clientAddr = req["addr"]
	})

	engine.Run("localhost:8080")
}
