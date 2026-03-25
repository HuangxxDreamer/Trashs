package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PointCloudFramesProcessed 记录处理过的点云帧数，可用于计算帧率
	PointCloudFramesProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gateway_pointcloud_frames_processed_total",
		Help: "The total number of processed point cloud frames",
	})

	// WebRTCBufferBytes 记录当前 3D DataChannel 的缓冲区积压字节数
	WebRTCBufferBytes = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "gateway_webrtc_buffer_bytes",
		Help: "Current size of the WebRTC 3D DataChannel buffer",
	})

	// DroppedFramesTotal 记录因为抗拥塞策略而主动丢弃的帧数
	DroppedFramesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_dropped_frames_total",
			Help: "Total number of dropped frames by type and reason",
		},
		[]string{"type", "reason"},
	)

	// MemoryPoolBorrowTotal 记录内存池借出次数（用于衡量池活跃度）
	MemoryPoolBorrowTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_memory_pool_borrow_total",
			Help: "Total number of buffers borrowed from the memory pool",
		},
		[]string{"pool_type"},
	)
)
