package robot

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// TimeoutHandler 提供用于处理机器人操作中超时的实用工具
type TimeoutHandler struct {
	logger *zap.Logger
}

// NewTimeoutHandler 创建一个新的超时处理器
func NewTimeoutHandler() *TimeoutHandler {
	return &TimeoutHandler{
		logger: zap.L(),
	}
}

// WithTimeout 使用超时执行函数
func (th *TimeoutHandler) WithTimeout(ctx context.Context, timeout time.Duration, operation func() error) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- operation()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		th.logger.Warn("操作超时", zap.Duration("timeout", timeout))
		return ctx.Err()
	}
}

// RetryWithTimeout 使用超时和指数退避重试操作
func (th *TimeoutHandler) RetryWithTimeout(ctx context.Context, maxRetries int, baseTimeout time.Duration, operation func() error) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		timeout := baseTimeout * time.Duration(1<<uint(i)) // 指数退避

		err := th.WithTimeout(ctx, timeout, operation)
		if err == nil {
			return nil
		}

		lastErr = err
		th.logger.Warn("操作失败，正在重试",
			zap.Int("attempt", i+1),
			zap.Int("max_retries", maxRetries),
			zap.Duration("timeout", timeout),
			zap.Error(err))

		if i < maxRetries-1 {
			// 重试前等待，带抖动
			waitTime := time.Duration(100*(1<<uint(i))) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(waitTime):
				continue
			}
		}
	}

	return lastErr
}

// CircuitBreaker 实现简单的断路器模式
type CircuitBreaker struct {
	maxFailures  int
	resetTimeout time.Duration
	failures     int
	lastFailTime time.Time
	state        CircuitState
	logger       *zap.Logger
}

type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// NewCircuitBreaker 创建一个新的断路器
func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
		state:        CircuitClosed,
		logger:       zap.L(),
	}
}

// Execute 通过断路器运行操作
func (cb *CircuitBreaker) Execute(operation func() error) error {
	if cb.state == CircuitOpen {
		if time.Since(cb.lastFailTime) > cb.resetTimeout {
			cb.state = CircuitHalfOpen
			cb.logger.Info("断路器转换为半开状态")
		} else {
			return ErrCircuitBreakerOpen
		}
	}

	err := operation()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

func (cb *CircuitBreaker) recordFailure() {
	cb.failures++
	cb.lastFailTime = time.Now()

	if cb.failures >= cb.maxFailures {
		cb.state = CircuitOpen
		cb.logger.Warn("断路器已打开",
			zap.Int("failures", cb.failures),
			zap.Int("max_failures", cb.maxFailures))
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
		cb.logger.Info("断路器已关闭")
	}
	cb.failures = 0
}

// GetState 返回当前断路器状态
func (cb *CircuitBreaker) GetState() CircuitState {
	return cb.state
}

// 自定义错误
var (
	ErrCircuitBreakerOpen = NewTimeoutError("断路器已打开")
	ErrOperationTimeout   = NewTimeoutError("操作超时")
)

// TimeoutError 表示与超时相关的错误
type TimeoutError struct {
	message string
}

func NewTimeoutError(message string) *TimeoutError {
	return &TimeoutError{message: message}
}

func (e *TimeoutError) Error() string {
	return e.message
}

func (e *TimeoutError) IsTimeout() bool {
	return true
}

// EmergencyStopManager 处理紧急停止场景
type EmergencyStopManager struct {
	stopCh  chan struct{}
	resetCh chan struct{}
	stopped bool
	logger  *zap.Logger
}

// NewEmergencyStopManager 创建一个新的紧急停止管理器
func NewEmergencyStopManager() *EmergencyStopManager {
	return &EmergencyStopManager{
		stopCh:  make(chan struct{}, 1),
		resetCh: make(chan struct{}, 1),
		logger:  zap.L(),
	}
}

// EmergencyStop 触发紧急停止
func (esm *EmergencyStopManager) EmergencyStop() {
	if !esm.stopped {
		esm.stopped = true
		select {
		case esm.stopCh <- struct{}{}:
			esm.logger.Warn("紧急停止已触发")
		default:
			// 通道已满，停止已触发
		}
	}
}

// Reset 重置紧急停止状态
func (esm *EmergencyStopManager) Reset() {
	if esm.stopped {
		esm.stopped = false
		select {
		case esm.resetCh <- struct{}{}:
			esm.logger.Info("紧急停止已重置")
		default:
			// 通道已满，重置已触发
		}
	}
}

// WaitForEmergencyStop 阻塞直到紧急停止被触发
func (esm *EmergencyStopManager) WaitForEmergencyStop() <-chan struct{} {
	return esm.stopCh
}

// WaitForReset 阻塞直到紧急停止被重置
func (esm *EmergencyStopManager) WaitForReset() <-chan struct{} {
	return esm.resetCh
}

// IsStopped 如果紧急停止处于活动状态则返回true
func (esm *EmergencyStopManager) IsStopped() bool {
	return esm.stopped
}
