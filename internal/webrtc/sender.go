package webrtc

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog/log"

	"dog-stream-gateway/internal/metrics"
	"dog-stream-gateway/internal/pool"
	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/pkg/config"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WebRTCSender 负责管理 P2P 连接、两个 DataChannel 以及数据推送。
// 它通过 HTTP 端口提供 WebSocket 信令服务，并在建连后循环消费 processCh。
type WebRTCSender struct {
	processCh chan *types.ProcessedFrame
	api       *webrtc.API
	pc        *webrtc.PeerConnection
	dc3D      *webrtc.DataChannel
	dc2D      *webrtc.DataChannel
	mu        sync.Mutex // 保护 PeerConnection 和 DataChannel 引用
}

// NewWebRTCSender 初始化一个流媒体发送器，但暂不启动 HTTP 监听
func NewWebRTCSender(processCh chan *types.ProcessedFrame) (*WebRTCSender, error) {
	// 配置 WebRTC UDP 端口范围
	s := webrtc.SettingEngine{}
	s.SetEphemeralUDPPortRange(config.Cfg.WebRTC.PortMin, config.Cfg.WebRTC.PortMax)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	return &WebRTCSender{
		processCh: processCh,
		api:       api,
	}, nil
}

// Start 启动 HTTP 信令服务器并进入等待
func (s *WebRTCSender) Start(ctx context.Context) {
	http.HandleFunc("/ws", s.signalingHandler)
	addr := fmt.Sprintf(":%d", config.Cfg.Server.Port)
	server := &http.Server{Addr: addr}

	log.Info().Str("Addr", addr).Msg("[WebRTC] 信令服务器已启动，等待前端通过 WebSocket 接入")

	// 开启消费 Goroutine（即使还没连上，也要清空管道，防止上游积压）
	go s.webrtcSenderGoroutine(ctx)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("[WebRTC] 信令服务器异常退出")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("[WebRTC] 收到退出信号，关闭信令服务...")
	server.Shutdown(context.Background())
	s.mu.Lock()
	if s.pc != nil {
		s.pc.Close()
	}
	s.mu.Unlock()
}

// signalingHandler 处理 WebSocket 连接，完成 SDP 交换
func (s *WebRTCSender) signalingHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] WebSocket 升级失败")
		return
	}
	defer conn.Close()

	// 初始化一个新的 PeerConnection
	s.mu.Lock()
	if s.pc != nil {
		log.Warn().Msg("[WebRTC] 发现旧的连接，正在替换")
		s.pc.Close()
	}

	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] 创建 PeerConnection 失败")
		s.mu.Unlock()
		return
	}
	s.pc = pc
	s.mu.Unlock()

	// -----------------------------------------------------
	// 核心架构要求：创建 2 个 DataChannel
	// -----------------------------------------------------

	// 1. DataChannel 1 (3D PointCloud)：极速传输，允许丢包，绝不阻塞
	var maxRetransmits uint16 = 0
	ordered := false
	dc3D, err := pc.CreateDataChannel("pointcloud", &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	})
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] 创建 3D DataChannel 失败")
		return
	}
	s.dc3D = dc3D
	dc3D.OnOpen(func() { log.Info().Msg("[WebRTC] 3D 点云 DataChannel 1 已打开") })

	// 2. DataChannel 2 (2D Map + Status)：可靠传输
	dc2D, err := pc.CreateDataChannel("gridmap", nil) // 默认 ordered:true, reliable
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] 创建 2D DataChannel 失败")
		return
	}
	s.dc2D = dc2D
	dc2D.OnOpen(func() { log.Info().Msg("[WebRTC] 2D 栅格 DataChannel 2 已打开") })

	// 交换 SDP
	for {
		offer := webrtc.SessionDescription{}
		if err := conn.ReadJSON(&offer); err != nil {
			log.Info().Err(err).Msg("[WebRTC] WebSocket 解析 SDP 失败或断开")
			break
		}

		if err := pc.SetRemoteDescription(offer); err != nil {
			log.Error().Err(err).Msg("[WebRTC] 设置 Remote SDP 失败")
			continue
		}

		answer, err := pc.CreateAnswer(nil)
		if err != nil {
			log.Error().Err(err).Msg("[WebRTC] 创建 Answer 失败")
			continue
		}

		if err := pc.SetLocalDescription(answer); err != nil {
			log.Error().Err(err).Msg("[WebRTC] 设置 Local SDP 失败")
			continue
		}

		if err := conn.WriteJSON(answer); err != nil {
			log.Error().Err(err).Msg("[WebRTC] 发送 Answer 失败")
			break
		}
	}
}

// webrtcSenderGoroutine 是网关的核心消费线程
// 它将 ProcessCh 的数据转化为二进制协议并投递到 WebRTC
func (s *WebRTCSender) webrtcSenderGoroutine(ctx context.Context) {
	thresholdBytes := uint64(config.Cfg.WebRTC.BufferThresholdKB * 1024)

	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-s.processCh:
			// 防御性处理：如果没有连接或 Channel 没打开，直接丢弃
			s.mu.Lock()
			pc := s.pc
			s.mu.Unlock()

			if pc == nil || pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
				s.freeFrame(frame)
				continue
			}

			if frame.Type == types.DataTypePointCloud {
				// -----------------------------------------------------
				// 工业级抗干扰策略：基于阈值的主动丢帧机制
				// -----------------------------------------------------
				if s.dc3D != nil && s.dc3D.ReadyState() == webrtc.DataChannelStateOpen {
					buffered := s.dc3D.BufferedAmount()
					metrics.WebRTCBufferBytes.Set(float64(buffered))

					if buffered > thresholdBytes {
						// 缓冲区积压过高，直接丢弃最新这帧点云，并触发告警
						log.Warn().
							Uint64("Buffered", buffered).
							Msg("[WebRTC] 3D DataChannel 拥塞，触发主动丢帧策略 (彻底抛弃 TCP 队头阻塞)")
						metrics.DroppedFramesTotal.WithLabelValues("pointcloud", "webrtc_buffer_full").Inc()
						s.freeFrame(frame)
						continue
					}

					// 将 float32 数组高速转换为紧凑的二进制字节流 (小端序)
					// 使用内存池中的 Buffer 来存放临时结果
					byteBuf := pool.GetByteBuffer()
					dataLen := len(frame.Points) * 4
					if cap(byteBuf.Data) < dataLen {
						byteBuf.Data = make([]byte, dataLen)
					} else {
						byteBuf.Data = byteBuf.Data[:dataLen]
					}

					for i, f := range frame.Points {
						binary.LittleEndian.PutUint32(byteBuf.Data[i*4:], math.Float32bits(f))
					}

					err := s.dc3D.Send(byteBuf.Data)
					if err != nil {
						log.Error().Err(err).Msg("[WebRTC] 发送 3D 数据失败")
					}

					pool.PutByteBuffer(byteBuf)
				}
			} else if frame.Type == types.DataTypeGridMap {
				// 2D 栅格地图，可靠传输
				if s.dc2D != nil && s.dc2D.ReadyState() == webrtc.DataChannelStateOpen {
					err := s.dc2D.Send(frame.MapData)
					if err != nil {
						log.Error().Err(err).Msg("[WebRTC] 发送 2D 数据失败")
					}
				}
			}

			// 归还底层大内存块，实现零 GC 压力循环
			s.freeFrame(frame)
		}
	}
}

// freeFrame 归还帧使用的底层内存
func (s *WebRTCSender) freeFrame(frame *types.ProcessedFrame) {
	if frame.Type == types.DataTypePointCloud {
		// 还原 Float32Buffer 结构并放回 Pool
		buf := &pool.Float32Buffer{Data: frame.Points}
		pool.PutFloat32Buffer(buf)
	} else if frame.Type == types.DataTypeGridMap {
		// 因为我们在 processing 里面做了 base64 的新分配，如果是池化分配的，这里应该 Put
		// 这里暂不复用 base64 的字节数组，交由 GC 回收（2D 频率低，影响较小）
	}
}
