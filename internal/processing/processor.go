package processing

import (
	"bytes"
	"context"

	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"

	"github.com/disintegration/imaging"
	"github.com/rs/zerolog/log"

	"dog-stream-gateway/internal/metrics"
	"dog-stream-gateway/internal/pool"
	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/pkg/config"
)

// Processor 结构体负责核心计算：清洗、滤波、坐标系转换。
// 它是连接 Ingestion 层和 Egress(WebRTC) 层的纽带。
type Processor struct {
	ingestCh  chan *types.RosRawFrame
	processCh chan *types.ProcessedFrame
	// 使用 sync.Once 确保初始化时的安全
	once sync.Once
}

// NewProcessor 创建一个新的数据处理中心
func NewProcessor(ingestCh chan *types.RosRawFrame, processCh chan *types.ProcessedFrame) *Processor {
	return &Processor{
		ingestCh:  ingestCh,
		processCh: processCh,
	}
}

// Start 启动后台消费 Goroutine，阻塞执行直至 ctx 被取消。
func (p *Processor) Start(ctx context.Context) {
	log.Info().Msg("[Processing] 启动数据清洗与降采样线程...")

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("[Processing] 收到退出信号，终止清洗循环。")
			return
		case rawFrame := <-p.ingestCh:
			// 当有新的原始帧到达时，分派给相应的处理函数
			if rawFrame.Type == types.DataTypePointCloud {
				p.handlePointCloud(rawFrame)
			} else if rawFrame.Type == types.DataTypeGridMap {
				p.handleGridMap(rawFrame)
			}
		}
	}
}

// handlePointCloud 处理原始的 3D 点云二进制数据。
//
// 性能优化要点：
// 1. 避免遍历全量数据去构建复杂的中间结构体。
// 2. 边解析边进行随机抽稀采样，仅保留 3000~8000 个点。
// 3. 从内存池中借用 float32 数组，避免内存碎片和 GC 开销。
func (p *Processor) handlePointCloud(raw *types.RosRawFrame) {
	// 使用 ROS 消息中的动态步长，应对激光雷达字段变更
	pointSize := raw.PointStep
	if pointSize == 0 {
		return
	}
	totalPoints := len(raw.RawData) / pointSize

	if totalPoints == 0 {
		return
	}

	// 计算采样率
	targetPoints := config.Cfg.Processing.MaxPoints
	if targetPoints > totalPoints {
		targetPoints = totalPoints
	}

	// 计算采样步长或概率（这里采用简化的步长采样，比纯随机快，且分布较均匀）
	step := float64(totalPoints) / float64(targetPoints)

	// 从内存池借用一个 float32 缓冲数组
	buf := pool.GetFloat32Buffer()
	outPoints := buf.Data

	// 局部坐标到 WGS84 的平移矩阵（占位符示例）
	// 在实际工业场景中，这里会根据锚点进行复杂的四元数旋转和平移。
	const offsetX, offsetY, offsetZ = 100.0, 200.0, 50.0

	for i := 0.0; i < float64(totalPoints); i += step {
		idx := int(i) * pointSize
		// 校验：确保加上动态偏移量后不会越界
		if idx+raw.OffsetX+4 > len(raw.RawData) ||
			idx+raw.OffsetY+4 > len(raw.RawData) ||
			idx+raw.OffsetZ+4 > len(raw.RawData) {
			break
		}

		// 解析原始二进制 (小端序)，使用动态偏移量
		xBits := binary.LittleEndian.Uint32(raw.RawData[idx+raw.OffsetX : idx+raw.OffsetX+4])
		yBits := binary.LittleEndian.Uint32(raw.RawData[idx+raw.OffsetY : idx+raw.OffsetY+4])
		zBits := binary.LittleEndian.Uint32(raw.RawData[idx+raw.OffsetZ : idx+raw.OffsetZ+4])

		x := math.Float32frombits(xBits)
		y := math.Float32frombits(yBits)
		z := math.Float32frombits(zBits)

		// 剔除无效点 (NaN)
		if math.IsNaN(float64(x)) || math.IsNaN(float64(y)) || math.IsNaN(float64(z)) {
			continue
		}

		// 极简坐标转换（此处模拟转换为某种全局系）
		// 在真正的实现中可引入 github.com/go-gnss/wgs84 库，这里为保持热路径极速，采用预计算偏移
		x += offsetX
		y += offsetY
		z += offsetZ

		// 加入到池化缓冲中，这里不再分配新内存，只要没超过预分配容量
		// 我们预先保留了 XYZ 以及伪造的 RGBA（全白），所以每点占 7 个 float32
		outPoints = append(outPoints, x, y, z, 255.0, 0.0, 0.0, 255.0)
	}

	buf.Data = outPoints

	processedFrame := &types.ProcessedFrame{
		Type:      types.DataTypePointCloud,
		Points:    buf.Data,
		Timestamp: raw.Timestamp,
	}

	select {
	case p.processCh <- processedFrame:
		metrics.PointCloudFramesProcessed.Inc()
	default:
		// 如果发送通道阻塞，归还借出的池，避免内存泄露
		pool.PutFloat32Buffer(buf)
		metrics.DroppedFramesTotal.WithLabelValues("pointcloud", "processCh_full").Inc()
		log.Warn().Msg("[Processing] ProcessCh 阻塞，主动丢弃一帧已清洗点云，抗背压。")
	}
}

// handleGridMap 处理 2D 栅格地图
// 它负责把一维的概率数组 (-1, 0-100) 转换为极小的 PNG 图像，然后转为 Base64 传递给 Egress。
func (p *Processor) handleGridMap(raw *types.RosRawFrame) {
	// 从 ROS RawFrame 中获取实际的动态地图分辨率
	width, height := raw.Width, raw.Height

	// 防止数据长度与声明的分辨率不匹配导致的越界
	if width <= 0 || height <= 0 || len(raw.RawData) < width*height {
		log.Warn().
			Int("Width", width).
			Int("Height", height).
			Int("DataLen", len(raw.RawData)).
			Msg("[Processing] 栅格地图数据不完整或分辨率异常，跳过处理。")
		return
	}

	// 1. 生成 2D 图像
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			val := int8(raw.RawData[y*width+x])
			var c color.NRGBA

			// ROS 中 -1 代表未知，0 代表空闲，100 代表障碍物
			if val == -1 {
				c = color.NRGBA{R: 128, G: 128, B: 128, A: 255} // 灰色
			} else if val == 0 {
				c = color.NRGBA{R: 255, G: 255, B: 255, A: 255} // 白色
			} else {
				c = color.NRGBA{R: 0, G: 0, B: 0, A: 255} // 黑色
			}
			img.SetNRGBA(x, y, c)
		}
	}

	// 2. 图像缩小（降采样以减少传输体积），缩放到最大 200x200 边界内
	// 使用 imaging.Fit 代替 Resize，以保持地图的原始宽高比，防止形变。
	smallImg := imaging.Fit(img, 200, 200, imaging.NearestNeighbor)

	// 3. PNG 压缩并获取字节流
	// 我们从内存池借一个 ByteBuffer，避免频繁 gc
	byteBuf := pool.GetByteBuffer()

	// 封装成 io.Writer 兼容形式
	writer := bytes.NewBuffer(byteBuf.Data[:0])
	err := png.Encode(writer, smallImg)
	if err != nil {
		log.Error().Err(err).Msg("[Processing] PNG 编码失败")
		pool.PutByteBuffer(byteBuf)
		return
	}

	// 注意，写入后切片的长度已经被改变了，我们需要拿到实际切片
	encodedBytes := writer.Bytes()

	// 为了修复内存泄漏：对池化内存中的数据进行深拷贝
	// 2D 地图频率低（约 1Hz），此处适度分配内存不会对 GC 造成明显压力
	mapDataCopy := make([]byte, len(encodedBytes))
	copy(mapDataCopy, encodedBytes)

	// 此时可以安全归还 byteBuf 到池中，因为我们已经完成了数据拷贝
	pool.PutByteBuffer(byteBuf)

	// 将拷贝后的独立数据放入 ProcessedFrame
	processedFrame := &types.ProcessedFrame{
		Type:      types.DataTypeGridMap,
		MapData:   mapDataCopy,
		Timestamp: raw.Timestamp,
	}

	select {
	case p.processCh <- processedFrame:
	default:
		metrics.DroppedFramesTotal.WithLabelValues("gridmap", "processCh_full").Inc()
		log.Warn().Msg("[Processing] ProcessCh 阻塞，丢弃 2D 栅格地图。")
	}
}
