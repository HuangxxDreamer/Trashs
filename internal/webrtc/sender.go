package webrtc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog/log"

	"dog-stream-gateway/internal/archive"
	"dog-stream-gateway/internal/metrics"
	"dog-stream-gateway/internal/pool"
	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/pkg/config"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WSMessage 定义了 WebSocket 信令消息格式，支持 SDP 交换和自定义 Action 指令
type WSMessage struct {
	Action string                     `json:"action,omitempty"` // 例如 "finish_mapping"
	SDP    *webrtc.SessionDescription `json:"sdp,omitempty"`    // 兼容原有的 SDP 交换
}

// WebRTCSender 负责管理 P2P 连接、两个 DataChannel 以及数据推送。
// 它通过 HTTP 端口提供 WebSocket 信令服务，并在建连后循环消费 processCh。
type WebRTCSender struct {
	processCh chan *types.ProcessedFrame
	api       *webrtc.API
	pc        *webrtc.PeerConnection
	dc3D      *webrtc.DataChannel
	dc2D      *webrtc.DataChannel
	mu        sync.RWMutex // 升级为读写锁，保护 PeerConnection 和 DataChannel 引用

	// 新增归档相关字段
	archiver     *archive.Archiver
	latestMapRaw []byte
	mapMu        sync.RWMutex
}

// NewWebRTCSender 初始化一个流媒体发送器，但暂不启动 HTTP 监听
func NewWebRTCSender(processCh chan *types.ProcessedFrame, archiver *archive.Archiver) (*WebRTCSender, error) {
	// 配置 WebRTC UDP 端口范围
	s := webrtc.SettingEngine{}
	s.SetEphemeralUDPPortRange(config.Cfg.WebRTC.PortMin, config.Cfg.WebRTC.PortMax)

	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	return &WebRTCSender{
		processCh: processCh,
		api:       api,
		archiver:  archiver,
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

	// 修复优雅退出：监听上下文完成信号以主动断开 WebSocket
	// 当 http.Server 停止或连接中断时，r.Context() 会被取消
	go func() {
		<-r.Context().Done()
		conn.Close()
	}()

	// 初始化一个新的 PeerConnection
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] 创建 PeerConnection 失败")
		return
	}

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

	// 2. DataChannel 2 (2D Map + Status)：可靠传输
	dc2D, err := pc.CreateDataChannel("gridmap", nil) // 默认 ordered:true, reliable
	if err != nil {
		log.Error().Err(err).Msg("[WebRTC] 创建 2D DataChannel 失败")
		return
	}

	// 在持有写锁的情况下，统一更新所有关键引用，确保原子性
	s.mu.Lock()
	if s.pc != nil {
		log.Warn().Msg("[WebRTC] 发现旧的连接，正在关闭旧连接")
		s.pc.Close()
	}
	s.pc = pc
	s.dc3D = dc3D
	s.dc2D = dc2D
	s.mu.Unlock()

	// 监听连接状态，及时清理僵尸资源
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Info().Str("State", state.String()).Msg("[WebRTC] 连接状态变更")
		if state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateFailed ||
			state == webrtc.PeerConnectionStateClosed {

			log.Warn().Msg("[WebRTC] 客户端连接已断开或失败，清理资源...")
			s.mu.Lock()
			// 只有当当前的 pc 确实是这个要关闭的 pc 时才置空，防止误删新接入的连接
			if s.pc == pc {
				s.pc = nil
				s.dc3D = nil
				s.dc2D = nil
			}
			s.mu.Unlock()
			pc.Close()
		}
	})

	dc3D.OnOpen(func() { log.Info().Msg("[WebRTC] 3D 点云 DataChannel 1 已打开") })
	dc2D.OnOpen(func() { log.Info().Msg("[WebRTC] 2D 栅格 DataChannel 2 已打开") })

	// 交换 SDP 与 Action 指令
	for {
		var msg WSMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Info().Err(err).Msg("[WebRTC] WebSocket 解析消息失败或断开")
			break
		}

		// 拦截 "finish_mapping" 指令
		if msg.Action == "finish_mapping" {
			log.Info().Msg("[WebRTC] 收到前端 'finish_mapping' 指令")
			s.mapMu.RLock()
			data := make([]byte, len(s.latestMapRaw))
			copy(data, s.latestMapRaw)
			s.mapMu.RUnlock()

			if len(data) > 0 {
				s.archiver.StartArchive(data)
			} else {
				log.Warn().Msg("[WebRTC] 当前缓存的地图数据为空，无法归档")
			}
			continue
		}

		// 处理 SDP 交换
		if msg.SDP != nil {
			offer := *msg.SDP
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

			resp := WSMessage{SDP: &answer}
			if err := conn.WriteJSON(resp); err != nil {
				log.Error().Err(err).Msg("[WebRTC] 发送 Answer 失败")
				break
			}
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
			// 获取当前连接快照
			s.mu.RLock()
			pc := s.pc
			dc3D := s.dc3D
			dc2D := s.dc2D
			s.mu.RUnlock()

			if pc == nil || pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
				s.freeFrame(frame)
				continue
			}

			if frame.Type == types.DataTypePointCloud {
				// -----------------------------------------------------
				// 工业级抗干扰策略：基于阈值的主动丢帧机制
				// -----------------------------------------------------
				if dc3D != nil && dc3D.ReadyState() == webrtc.DataChannelStateOpen {
					buffered := dc3D.BufferedAmount()
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

					err := dc3D.Send(byteBuf.Data)
					if err != nil {
						log.Error().Err(err).Msg("[WebRTC] 发送 3D 数据失败")
					}

					pool.PutByteBuffer(byteBuf)
				}
			} else if frame.Type == types.DataTypeGridMap {
				// 缓存最新地图原始字节流用于归档 (PNG 格式)
				s.mapMu.Lock()
				s.latestMapRaw = make([]byte, len(frame.MapData))
				copy(s.latestMapRaw, frame.MapData)
				s.mapMu.Unlock()

				// 2D 栅格地图推送给前端，前端 img 标签需要 Base64 (或带有 data:image/png;base64 前缀)
				// 注意：如果前端 README 约定只推纯 base64，则此处不加前缀
				if dc2D != nil && dc2D.ReadyState() == webrtc.DataChannelStateOpen {
					b64Len := base64.StdEncoding.EncodedLen(len(frame.MapData))
					b64Data := make([]byte, b64Len)
					base64.StdEncoding.Encode(b64Data, frame.MapData)

					err := dc2D.Send(b64Data)
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
