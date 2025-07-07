package mem

import (
	"sync"
	"sync/atomic"
)

var (
	bufferPoolingThreshold = 1 << 12 // 4KB，适合传感器数据优化

	bufferObjectPool = sync.Pool{New: func() any { return new(buffer) }}
	refObjectPool    = sync.Pool{New: func() any { return new(atomic.Int32) }}
)

type Buffer interface {
	// ReadOnlyData 返回底层字节切片，
	ReadOnlyData() []byte
	// Ref 增加此Buffer的引用计数。
	Ref()
	// Free 减少此Buffer的引用计数，如果计数因此调用而达到0，则释放底层字节切片。
	Free()
	// Len 返回Buffer的大小。
	Len() int

	read([]byte) (int, Buffer)
	split(int) (Buffer, Buffer)
}

// NewBuffer 使用给定数据初始化Buffer
// 并将计数器初始化为1。当Buffer持有的所有引用都被释放时，
// Buffer会返回到池中。
func NewBuffer(data *[]byte, pool BufferPool) Buffer {
	if pool == nil && IsLessBufferPoolThreshold(cap(*data)) {
		return (SliceBuffer)(*data)
	}

	b := newBuffer()
	b.originData = data
	b.data = *data
	b.pool = pool
	b.refs = refObjectPool.Get().(*atomic.Int32)
	b.refs.Add(1)
	return b
}

// Split 修改接收者以指向前n个字节，同时
// 返回对剩余字节的新引用。返回的Buffer
// 功能就像使用Ref()获取的普通引用一样。
func Split(buf Buffer, n int) (Buffer, Buffer) {
	return buf.split(n)
}

// Read 从给定的Buffer读取字节到提供的切片中。
func Read(buf Buffer, data []byte) (int, Buffer) {
	return buf.read(data)
}

type buffer struct {
	originData *[]byte
	data       []byte
	refs       *atomic.Int32
	pool       BufferPool
}

func newBuffer() *buffer {
	return bufferObjectPool.Get().(*buffer)
}

func (b *buffer) ReadOnlyData() []byte {
	if b.refs == nil {
		panic("无法读取已释放的缓冲区")
	}
	return b.data
}

func (b *buffer) read(buf []byte) (int, Buffer) {
	if b.refs == nil {
		panic("无法读取已释放的缓冲区")
	}

	n := copy(buf, b.data)
	if n == len(b.data) {
		b.Free()
		return n, nil
	}

	b.data = b.data[n:]
	return n, b
}

func (b *buffer) Ref() {
	if b.refs == nil {
		panic("无法引用已释放的缓冲区")
	}
	b.refs.Add(1)
}

func (b *buffer) Free() {
	if b.refs == nil {
		panic("无法释放已释放的缓冲区")
	}

	refs := b.refs.Add(-1)
	switch {
	case refs > 0:
		return
	case refs == 0:
		if b.pool != nil {
			b.pool.Put(b.originData)
		}

		refObjectPool.Put(b.refs)
		b.originData = nil
		b.data = nil
		b.refs = nil
		b.pool = nil
		bufferObjectPool.Put(b)
	default:
		panic("无法释放已释放的缓冲区")
	}
}

func (b *buffer) Len() int {
	return len(b.ReadOnlyData())
}

func (b *buffer) split(n int) (Buffer, Buffer) {
	if b.refs == nil {
		panic("无法分割已释放的缓冲区")
	}

	b.refs.Add(1)
	split := newBuffer()
	split.originData = b.originData
	split.data = b.data[n:]
	split.refs = b.refs
	split.pool = b.pool

	b.data = b.data[:n]

	return b, split
}

// IsLessBufferPoolThreshold 用于确定当前所需大小
// 是否达到采用缓冲区的阈值大小。
func IsLessBufferPoolThreshold(size int) bool {
	return size <= bufferPoolingThreshold
}

// SliceBuffer 是一个包装字节切片的Buffer实现。它提供了
// 读取、分割和管理字节切片的方法。
// 当所需大小未达到阈值时使用它。
type SliceBuffer []byte

func (s SliceBuffer) ReadOnlyData() []byte {
	return s
}

func (s SliceBuffer) Ref() {}

func (s SliceBuffer) Free() {}

func (s SliceBuffer) Len() int {
	return len(s)
}

func (s SliceBuffer) read(buf []byte) (int, Buffer) {
	n := copy(buf, s)
	if n == len(s) {
		return n, nil
	}

	return n, s[n:]
}

func (s SliceBuffer) split(n int) (Buffer, Buffer) {
	return s[:n], s[n:]
}

type emptyBuffer struct{}

func (e emptyBuffer) ReadOnlyData() []byte {
	return nil
}

func (e emptyBuffer) Ref()  {}
func (e emptyBuffer) Free() {}

func (e emptyBuffer) Len() int {
	return 0
}

func (e emptyBuffer) split(int) (left, right Buffer) {
	return e, e
}

func (e emptyBuffer) read([]byte) (int, Buffer) {
	return 0, e
}
