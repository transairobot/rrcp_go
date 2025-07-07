package robot

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"robot/protocol"
)

func TestServerClientIntegration(t *testing.T) {
	// 为测试设置日志记录器
	logger, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(logger)

	// 创建服务器配置
	config := &protocol.Config{
		ServerToClientFrequency: 5,
		ClientToServerFrequency: 10,
	}

	// 创建并启动服务器
	server := NewServer(config)
	serverAddr := "localhost:18080"

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	go func() {
		if err := server.Serve(serverAddr); err != nil {
			if serverCtx.Err() == nil { // 仅在未取消时记录
				t.Errorf("服务器错误: %v", err)
			}
		}
	}()

	time.Sleep(500 * time.Millisecond)

	client := NewClient()

	// 测试自定义动作处理器
	actionReceived := make(chan protocol.Action, 10)
	testHandler := &TestActionHandler{
		receivedActions: actionReceived,
		t:               t,
	}
	client.SetActionHandler(testHandler.HandleAction)

	err := client.Connect(serverAddr)
	if err != nil {
		t.Fatalf("无法将客户端连接到服务器: %v", err)
	}

	time.Sleep(1 * time.Second)

	clients := server.GetConnectedClients()
	if len(clients) == 0 {
		t.Fatalf("未找到已连接的客户端进行测试")
	}
	clientAddr := clients[0]

	// 从服务器发送一些动作
	server.SendAction(clientAddr, "move", map[string]interface{}{
		"x": 10.5,
		"y": 20.3,
	}, 1, false, 5000) // priority=1, requireAck=false, timeout=5000ms

	server.SendAction(clientAddr, "set_speed", map[string]interface{}{
		"speed": 75.0,
	}, 1, false, 5000)

	// 等待接收动作
	timeout := time.After(5 * time.Second)
	actionsReceived := 0
	expectedActions := 2

	for actionsReceived < expectedActions {
		select {
		case action := <-actionReceived:
			t.Logf("收到动作: %s，参数: %v", action.Type, action.Parameters)
			actionsReceived++
		case <-timeout:
			t.Fatalf("等待动作超时。收到 %d/%d 个动作", actionsReceived, expectedActions)
		}
	}

	// 清理
	if err := client.Disconnect(); err != nil {
		t.Errorf("断开客户端连接时出错: %v", err)
	}

	if err := server.Stop(); err != nil {
		t.Errorf("停止服务器时出错: %v", err)
	}
}

func TestTimeoutHandler(t *testing.T) {
	timeout := 1 * time.Second
	th := NewTimeoutHandler()

	callbackExecuted := make(chan bool, 1)

	operation := func() error {
		callbackExecuted <- true
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := th.WithTimeout(ctx, timeout, operation)
	if err != nil {
		t.Errorf("期望操作成功，但得到错误: %v", err)
	}

	select {
	case <-callbackExecuted:
		// 预期 - 操作已执行
	case <-time.After(2 * time.Second):
		t.Error("操作未执行")
	}
}

func TestCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(3, 1*time.Second)

	attempts := 0
	operation := func() error {
		attempts++
		if attempts < 3 {
			return &TestError{message: "模拟失败"}
		}
		return nil // 第3次尝试成功
	}

	// 前两次尝试应该失败
	err := cb.Execute(operation)
	if err == nil {
		t.Error("期望第一次尝试失败")
	}

	err = cb.Execute(operation)
	if err == nil {
		t.Error("期望第二次尝试失败")
	}

	// 第三次尝试应该成功
	err = cb.Execute(operation)
	if err != nil {
		t.Errorf("期望第三次尝试成功，但得到错误: %v", err)
	}
}

type TestActionHandler struct {
	receivedActions chan protocol.Action
	t               *testing.T
}

func (h *TestActionHandler) HandleAction(action protocol.Action) error {
	h.t.Logf("测试处理器收到动作: %s", action.Type)
	h.receivedActions <- action
	return nil
}

type TestError struct {
	message string
}

func (e *TestError) Error() string {
	return e.message
}

func BenchmarkServerClientCommunication(b *testing.B) {
	// 设置
	logger, _ := zap.NewDevelopment()
	zap.ReplaceGlobals(logger)

	config := &protocol.Config{
		ServerToClientFrequency: 50,
		ClientToServerFrequency: 100,
	}

	server := NewServer(config)
	serverAddr := "localhost:18081"

	go func() {
		server.Serve(serverAddr)
	}()

	time.Sleep(100 * time.Millisecond)

	client := NewClient()
	if err := client.Connect(serverAddr); err != nil {
		b.Fatalf("连接失败: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()

	// 获取基准测试用的已连接客户端
	clients := server.GetConnectedClients()
	if len(clients) == 0 {
		b.Fatalf("未找到已连接的客户端进行基准测试")
	}
	clientAddr := clients[0]

	// 基准测试动作发送
	for i := 0; i < b.N; i++ {
		server.SendAction(clientAddr, "test", map[string]interface{}{
			"iteration": i,
		}, 1, false, 5000) // priority=1, requireAck=false, timeout=5000ms
	}

	// 清理
	client.Disconnect()
	server.Stop()
}
