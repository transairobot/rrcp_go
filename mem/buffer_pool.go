package mem

import (
	"sync"
)

// BufferPool 是一个具有各种缓冲区大小的自管理池。
type BufferPool interface {
	// Get 返回指定大小的缓冲区。
	Get(size int) *[]byte
	// Put 将缓冲区返回到池中。
	Put(buffer *[]byte)
}

type PoolConfig struct {
	MaxPoolSize int
}

var DefaultConfig = &PoolConfig{
	MaxPoolSize: 1024 * 1024 * 8, // 8MB，支持高分辨率图像
}

var defaultPool bufferPool

// 为机器人数据传输优化的缓冲区大小
var bufferPoolSizes = []int{
	1 << 7,  // 128B - 小消息
	1 << 8,  // 256B - 协议头
	1 << 9,  // 512B - 传感器数据
	1 << 10, // 1KB - 小型传感器数据包
	1 << 11, // 2KB - 配置信息
	1 << 12, // 4KB - 复合传感器数据
	1 << 13, // 8KB - 小图像或压缩数据
	1 << 14, // 16KB - 中等图像数据
	1 << 15, // 32KB - 较大图像数据
	1 << 16, // 64KB - JPEG图像(320x240)
	1 << 17, // 128KB - 高质量图像
	1 << 18, // 256KB - 大分辨率图像
	1 << 19, // 512KB - 高分辨率图像
	1 << 20, // 1MB - 非常大的图像
	1 << 21, // 2MB - 4K图像数据
	1 << 22, // 4MB - 高质量4K图像
	1 << 23, // 8MB - 最大支持大小
}

type bufferPool struct {
	config  *PoolConfig
	pools   []*sync.Pool
	maxSize int
}

func init() {
	defaultPool.config = DefaultConfig
	defaultPool.maxSize = bufferPoolSizes[len(bufferPoolSizes)-1]
	defaultPool.pools = make([]*sync.Pool, len(bufferPoolSizes))

	for i := range bufferPoolSizes {
		size := bufferPoolSizes[i]
		defaultPool.pools[i] = &sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, size)
				return &buf
			},
		}
	}
}

// DefaultBufferPool 返回内存池
func DefaultBufferPool() BufferPool {
	return &defaultPool
}

// Get 返回指定大小的缓冲区。
func (p *bufferPool) Get(size int) *[]byte {
	if size <= 0 {
		return &[]byte{}
	}

	if i, exactMatch := p.findPool(size); exactMatch {
		buf := p.pools[i].Get().(*[]byte)
		*buf = (*buf)[0:size]
		return buf
	}

	index := p.findBestFitPool(size)
	if index >= 0 {
		buf := p.pools[index].Get().(*[]byte)
		*buf = (*buf)[0:size]
		return buf
	}

	buf := make([]byte, size)
	return &buf
}

// Put 将缓冲区返回到池中。
func (p *bufferPool) Put(buffer *[]byte) {
	if buffer == nil {
		return
	}

	size := cap(*buffer)
	if size <= 0 || size > p.maxSize {
		return
	}

	*buffer = (*buffer)[:0]

	if index, exact := p.findPool(size); exact {
		p.pools[index].Put(buffer)
		return
	}

	index := p.findClosestPool(size)
	if index >= 0 {
		p.pools[index].Put(buffer)
	}
}

func (p *bufferPool) findPool(size int) (int, bool) {
	for i, poolSize := range bufferPoolSizes {
		if size == poolSize {
			return i, true
		}
	}
	return -1, false
}

func (p *bufferPool) findBestFitPool(size int) int {
	for i, poolSize := range bufferPoolSizes {
		if size <= poolSize {
			return i
		}
	}
	return -1
}

func (p *bufferPool) findClosestPool(size int) int {
	for i := len(bufferPoolSizes) - 1; i >= 0; i-- {
		if size >= bufferPoolSizes[i] {
			return i
		}
	}
	return 0
}
