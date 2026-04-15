package pool

import (
	"dog-stream-gateway/internal/metrics"
	"sync"
)

// MemoryPool 是整个系统零 GC 压力的命脉。
// 整个网关在 10Hz 甚至更高频率下，要传输几万点或者几十 KB 图像数据，
// 如果每次都 make([]float32) 或者 make([]byte)，将会导致海量 GC 停顿，从而影响 WebRTC 的推流。
// 我们使用 sync.Pool 为大块内存建立对象池。

// 3D 点云最大支持的点数，由于我们只取 3000~8000，但为防止突发，分配的容量可以稍微大一点
// 8000 个点，每个点 7 个 float32 (X,Y,Z, R,G,B,A) = 56000 个 float32
const maxFloat32Capacity = 60000

// Float32Buffer 专门用于包装 3D 点云处理后的浮点数组
type Float32Buffer struct {
	Data []float32
}

// ByteBuffer 专门用于包装 2D 栅格地图压缩后的 PNG 字节数组
// 以及可能的中间临时缓冲
type ByteBuffer struct {
	Data []byte
}

var (
	// float32Pool 用于缓存 Float32Buffer
	float32Pool = sync.Pool{
		New: func() interface{} {
			// 在初次分配时，直接预留足够大的底层数组，避免使用过程中的扩容
			return &Float32Buffer{
				Data: make([]float32, 0, maxFloat32Capacity),
			}
		},
	}

	// bytePool 用于缓存 ByteBuffer，初始容量可以给 100KB，绝大部分压缩后的栅格地图在这个大小以内
	bytePool = sync.Pool{
		New: func() interface{} {
			return &ByteBuffer{
				Data: make([]byte, 0, 100*1024),
			}
		},
	}
)

// GetFloat32Buffer 从内存池借出一个 float32 缓冲区
// 借出的切片长度(len)已被重置为0，但容量(cap)保持。
// 这样在 append 操作时，能真正实现零分配。
func GetFloat32Buffer() *Float32Buffer {
	buf := float32Pool.Get().(*Float32Buffer)
	buf.Data = buf.Data[:0] // 重置长度，复用底层内存
	metrics.MemoryPoolBorrowTotal.WithLabelValues("float32").Inc()
	return buf
}

// PutFloat32Buffer 将使用完毕的 float32 缓冲区归还到内存池
func PutFloat32Buffer(buf *Float32Buffer) {
	if buf != nil {
		float32Pool.Put(buf)
	}
}

// GetByteBuffer 从内存池借出一个 byte 缓冲区 ，初始容量为 100KB
// 借出的切片长度(len)已被重置为0，但容量(cap)保持。
// 这样在 append 操作时，能真正实现零分配。
func GetByteBuffer() *ByteBuffer {
	buf := bytePool.Get().(*ByteBuffer)
	buf.Data = buf.Data[:0] // 重置长度，复用底层内存
	metrics.MemoryPoolBorrowTotal.WithLabelValues("byte").Inc()
	return buf
}

// PutByteBuffer 将使用完毕的 byte 缓冲区归还到内存池
func PutByteBuffer(buf *ByteBuffer) {
	if buf != nil {
		bytePool.Put(buf)
	}
}

// PreAllocate 预分配指定数量的缓冲区，填入池中
// 建议在 main 函数启动时调用，一次性分配好（例如预先分配 30 个），避免在运行时的瞬时分配
func PreAllocate(count int) {
	// 临时切片用于暂存，避免 Get 的时候立马从 Pool 里拿出来又放进去
	tempFloats := make([]*Float32Buffer, count)
	tempBytes := make([]*ByteBuffer, count)

	for i := 0; i < count; i++ {
		tempFloats[i] = GetFloat32Buffer()
		tempBytes[i] = GetByteBuffer()
	}

	for i := 0; i < count; i++ {
		PutFloat32Buffer(tempFloats[i])
		PutByteBuffer(tempBytes[i])
	}
}
