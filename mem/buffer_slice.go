package mem

import "io"

const (
	// 32 KiB 是 io.Copy 使用的大小。
	readAllBufSize = 32 * 1024
)

type BufferSlice []Buffer

// Len 返回此切片中所有Buffer长度的总和。
func (s BufferSlice) Len() int {
	length := 0
	for _, b := range s {
		length += b.Len()
	}

	return length
}

// Free 对切片中的每个Buffer调用Buffer.Free()。
func (s BufferSlice) Free() {
	for _, b := range s {
		b.Free()
	}
}

// Ref 对切片中的每个buffer调用Ref。
func (s BufferSlice) Ref() {
	for _, b := range s {
		b.Ref()
	}
}

// Materialize 将所有底层Buffer的数据连接成一个
// 连续的缓冲区，使用CopyTo。
func (s BufferSlice) Materialize() []byte {
	l := s.Len()
	if l == 0 {
		return nil
	}
	out := make([]byte, l)
	s.CopyTo(out)
	return out
}

// MaterializeToBuffer 类似于Materialize，只是
// 它使用需要池化的单个缓冲区。
//
// 特殊处理只有一个Buffer的情况，通过增加引用计数而不复制。
// 处理空BufferSlice的情况，返回一个特殊的emptyBuffer
// 从池中获取正确大小的缓冲区以减少内存分配。
func (s BufferSlice) MaterializeToBuffer(pool BufferPool) Buffer {
	if len(s) == 1 {
		s[0].Ref()
		return s[0]
	}
	sLen := s.Len()
	if sLen == 0 {
		return emptyBuffer{}
	}
	buf := pool.Get(sLen)
	s.CopyTo(*buf)
	return NewBuffer(buf, pool)
}

// CopyTo 将底层缓冲区的数据复制到给定的缓冲区dst，
// 返回复制的字节数。语义与内置copy函数相同；
// 它将复制尽可能多的字节，当dst已满或s用完数据时停止，
// 并返回s.Len()和len(dst)的最小值。
func (s BufferSlice) CopyTo(dst []byte) int {
	off := 0
	for _, b := range s {
		off += copy(dst[off:], b.ReadOnlyData())
	}
	return off
}

// Copy 创建一个具有给定数据的Buffer，
// 且Buffer的引用计数为1。
func Copy(data []byte, pool BufferPool) Buffer {
	if IsLessBufferPoolThreshold(len(data)) {
		buf := make(SliceBuffer, len(data))
		copy(buf, data)
		return buf
	}

	buf := pool.Get(len(data))
	copy(*buf, data)
	return NewBuffer(buf, pool)
}

// NewReader 在引用每个底层缓冲区后，
// 返回输入切片的新Reader。
func (s BufferSlice) NewReader() Reader {
	s.Ref()
	return &sliceReader{
		data: s,
		len:  s.Len(),
	}
}

type Reader interface {
	io.Reader
	io.ByteReader
	// Close 释放底层BufferSlice，永远不会返回错误。
	// 后续对Read的调用将返回(0, io.EOF)。
	Close() error
	// Remain 返回切片中未读字节的数量。
	Remain() int
}

type sliceReader struct {
	data      BufferSlice // 引用的BufferSlice
	len       int         // 剩余未读字节数
	bufferIdx int         // 当前正在读取的缓冲区中的偏移量
}

func (s *sliceReader) Read(buf []byte) (n int, err error) {
	if s.len == 0 {
		return 0, io.EOF
	}

	// 如果目标切片长度不为零且
	// 当前缓冲区的剩余长度不为零，则读取数据。
	for len(buf) != 0 && s.len != 0 {
		data := s.data[0].ReadOnlyData()
		cp := copy(buf, data[s.bufferIdx:])
		s.len -= cp
		s.bufferIdx += cp
		n += cp
		buf = buf[cp:]

		s.freeFirstBufferIfEmpty()
	}

	return n, nil
}

func (s *sliceReader) ReadByte() (byte, error) {
	if s.len == 0 {
		return 0, io.EOF
	}

	// 切片中可能有任意数量的空缓冲区，清除它们直到
	// 达到非空缓冲区。这保证会退出，因为r.len不为0。
	for s.freeFirstBufferIfEmpty() {
	}

	b := s.data[0].ReadOnlyData()[s.bufferIdx]
	s.bufferIdx++
	s.len--
	// 如果读取了最后一个字节，释放切片中的第一个缓冲区
	s.freeFirstBufferIfEmpty()
	return b, nil
}

func (s *sliceReader) Close() error {
	s.data.Free()
	s.data = nil
	s.len = 0
	return nil
}

func (s *sliceReader) Remain() int {
	return s.len
}

// freeFirstBufferIfEmpty 检查并释放已完成的缓冲区，
// 自动移动到下一个。
func (s *sliceReader) freeFirstBufferIfEmpty() bool {
	// 如果没有缓冲区可释放则返回false，
	// 如果当前缓冲区尚未读完（bufferIdx不等于缓冲区长度）则返回false。
	if len(s.data) == 0 || s.bufferIdx != len(s.data[0].ReadOnlyData()) {
		return false
	}

	// 读取完后释放Buffer。
	s.data[0].Free()
	// 重置索引，准备读取下一个缓冲区。
	s.data = s.data[1:]
	s.bufferIdx = 0
	return true
}

var _ io.Writer = (*writer)(nil)

type writer struct {
	buffers *BufferSlice
	pool    BufferPool
}

func (w writer) Write(p []byte) (n int, err error) {
	b := Copy(p, w.pool)
	*w.buffers = append(*w.buffers, b)
	return b.Len(), nil
}

// NewWriter 返回一个封装BufferSlice和BufferPool以实现
// io.Writer接口的writer。每次调用Write都会将给定的
// BufferSlice和BufferPool的内容复制到给定池中的新Buffer中，
// 并将该Buffer添加到给定的BufferSlice中。
func NewWriter(buffers *BufferSlice, pool BufferPool) io.Writer {
	return &writer{
		buffers: buffers,
		pool:    pool,
	}
}

func ReadAll(r io.Reader, pool BufferPool) (BufferSlice, error) {
	var result BufferSlice
	if wt, ok := r.(io.WriterTo); ok {
		// 这种方式更优，因为wt知道它想要写入的块的大小，
		// 因此我们可以分配最佳大小的缓冲区来容纳它们。
		// 例如，可能是一个大块，我们不会将其分成小块。
		w := NewWriter(&result, pool)
		_, err := wt.WriteTo(w)
		return result, err
	}
nextBuffer:
	for {
		buf := pool.Get(readAllBufSize)
		// 我们请求了32KiB，但可能得到了更大的缓冲区。
		// 如果是这种情况，就使用它的全部容量。
		*buf = (*buf)[:cap(*buf)]
		usedCap := 0
		for {
			n, err := r.Read((*buf)[usedCap:])
			usedCap += n
			if err != nil {
				if usedCap == 0 {
					// 这个缓冲区中没有内容，将其放回
					pool.Put(buf)
				} else {
					*buf = (*buf)[:usedCap]
					result = append(result, NewBuffer(buf, pool))
				}
				if err == io.EOF {
					err = nil
				}
				return result, err
			}
			if len(*buf) == usedCap {
				result = append(result, NewBuffer(buf, pool))
				continue nextBuffer
			}
		}
	}
}
