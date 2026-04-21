package ingestion

import (
	"context"
	"time"

	nav_msgs "dog-stream-gateway/internal/msgs/nav_msgs/msg"
	sensor_msgs "dog-stream-gateway/internal/msgs/sensor_msgs/msg"

	"github.com/rs/zerolog/log"
	"github.com/tiiuae/rclgo/pkg/rclgo"

	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/pkg/config"
)

// IngestionManager 是 ROS2 数据接入层的核心管理器，负责：
// 1. 初始化 ROS2 rclgo 运行环境和节点。
// 2. 订阅 /rtabmap/cloud_map 和 /rtabmap/grid_map 等话题。
// 3. 接收到消息后，进行极少量的解析和直接复制，并通过 ingestCh 推送到 Processing 层。
type IngestionManager struct {
	rclCtx   *rclgo.Context
	node     *rclgo.Node
	subCloud *rclgo.Subscription
	subGrid  *rclgo.Subscription
	ingestCh chan *types.RosRawFrame
}

// NewIngestionManager 构造一个新的 IngestionManager 实例，并关联数据下发的通道。
func NewIngestionManager(ingestCh chan *types.RosRawFrame) *IngestionManager {
	return &IngestionManager{
		ingestCh: ingestCh,
	}
}

// Start 负责真正启动 ROS2 节点，建立连接并开始订阅话题。
// 它是一个阻塞方法，使用 rclgo.Spin 等待消息。
// 使用 context.Context 使得系统能够随时中断连接。
func (im *IngestionManager) Start(ctx context.Context) error {
	var err error

	// 1. 初始化 ROS2 Context
	// 这里可以传入命令行参数，我们暂传空
	err = rclgo.Init(nil)
	if err != nil {
		log.Error().Err(err).Msg("[Ingestion] rclgo.Init 失败")
		return err
	}
	defer rclgo.Uninit()

	im.rclCtx, err = rclgo.NewContext(0, nil)
	if err != nil {
		log.Error().Err(err).Msg("[Ingestion] 创建 ROS2 Context 失败")
		return err
	}
	defer im.rclCtx.Close()

	// 2. 创建 ROS2 节点
	im.node, err = im.rclCtx.NewNode(config.Cfg.ROS.NodeName, config.Cfg.ROS.Namespace)
	if err != nil {
		log.Error().Err(err).Msg("[Ingestion] 创建 ROS2 节点失败")
		return err
	}
	log.Info().Str("Node", config.Cfg.ROS.NodeName).Msg("[Ingestion] ROS2 节点创建成功")

	// 3. 订阅 3D 点云（PointCloud2）
	subOpts := rclgo.NewDefaultSubscriptionOptions()
	// QoS 配置：传感器数据通常使用 BestEffort
	subOpts.Qos.Reliability = rclgo.ReliabilityBestEffort

	im.subCloud, err = im.node.NewSubscription(
		config.Cfg.ROS.TopicCloud,
		sensor_msgs.PointCloud2TypeSupport,
		subOpts,
		func(s *rclgo.Subscription) {
			var msg sensor_msgs.PointCloud2
			_, err := s.TakeMessage(&msg)
			if err != nil {
				return
			}
			im.onPointCloud2(&msg)
		},
	)
	if err != nil {
		log.Error().Err(err).Str("Topic", config.Cfg.ROS.TopicCloud).Msg("[Ingestion] 订阅 PointCloud2 失败")
		return err
	}
	log.Info().Str("Topic", config.Cfg.ROS.TopicCloud).Msg("[Ingestion] 已订阅 PointCloud2")

	// 4. 订阅 2D 栅格地图（OccupancyGrid）
	gridSubOpts := rclgo.NewDefaultSubscriptionOptions()
	gridSubOpts.Qos.Reliability = rclgo.ReliabilityReliable // 地图通常需要可靠传输

	im.subGrid, err = im.node.NewSubscription(
		config.Cfg.ROS.TopicGrid,
		nav_msgs.OccupancyGridTypeSupport,
		gridSubOpts,
		func(s *rclgo.Subscription) {
			var msg nav_msgs.OccupancyGrid
			_, err := s.TakeMessage(&msg)
			if err != nil {
				return
			}
			im.onOccupancyGrid(&msg)
		},
	)
	if err != nil {
		log.Error().Err(err).Str("Topic", config.Cfg.ROS.TopicGrid).Msg("[Ingestion] 订阅 OccupancyGrid 失败")
		return err
	}
	log.Info().Str("Topic", config.Cfg.ROS.TopicGrid).Msg("[Ingestion] 已订阅 OccupancyGrid")

	// 5. 启动节点 Spin
	log.Info().Msg("[Ingestion] ROS2 节点开始 Spin...")

	// 在一个新的 goroutine 中处理上下文取消，以停止 Spin
	go func() {
		<-ctx.Done()
		log.Info().Msg("[Ingestion] 收到退出信号，停止 ROS2 Context...")
		if im.rclCtx != nil {
			im.rclCtx.Close() // 这会打断 Spin
		}
	}()

	err = im.rclCtx.Spin(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("[Ingestion] ROS2 Spin 结束")
	}

	im.Stop()
	return nil
}

// Stop 用于主动释放 ROS 相关的资源
func (im *IngestionManager) Stop() {
	if im.subCloud != nil {
		im.subCloud.Close()
	}
	if im.subGrid != nil {
		im.subGrid.Close()
	}
	if im.node != nil {
		im.node.Close()
	}
	log.Info().Msg("[Ingestion] ROS2 节点已完全关闭")
}

// onPointCloud2 是传感器回调，接收到原始二进制数据。
func (im *IngestionManager) onPointCloud2(msg *sensor_msgs.PointCloud2) {
	// 动态解析 fields 偏移量，以应对激光雷达数据结构变化
	var offsetX, offsetY, offsetZ int
	for _, field := range msg.Fields {
		switch field.Name {
		case "x":
			offsetX = int(field.Offset)
		case "y":
			offsetY = int(field.Offset)
		case "z":
			offsetZ = int(field.Offset)
		}
	}

	// rclgo 生成的 PointCloud2.Data 是 []uint8 (即 []byte)
	frame := &types.RosRawFrame{
		Type:      types.DataTypePointCloud,
		RawData:   msg.Data,
		OffsetX:   offsetX,
		OffsetY:   offsetY,
		OffsetZ:   offsetZ,
		PointStep: int(msg.PointStep),
		Timestamp: time.Now(),
	}

	// 尝试写入通道，如果通道积压，不阻塞，避免影响底层 DDS 的接收缓冲。
	select {
	case im.ingestCh <- frame:
	default:
		log.Warn().Msg("[Ingestion] IngestCh 阻塞，丢弃了一帧 3D 点云")
	}
}

// onOccupancyGrid 栅格地图的回调。
func (im *IngestionManager) onOccupancyGrid(msg *nav_msgs.OccupancyGrid) {
	// rclgo 生成的 OccupancyGrid.Data 是 []int8
	// 我们需要转为 []byte
	dataBytes := make([]byte, len(msg.Data))
	for i, v := range msg.Data {
		dataBytes[i] = byte(v)
	}

	frame := &types.RosRawFrame{
		Type:      types.DataTypeGridMap,
		RawData:   dataBytes,
		Width:     int(msg.Info.Width),
		Height:    int(msg.Info.Height),
		Timestamp: time.Now(),
	}

	select {
	case im.ingestCh <- frame:
	default:
		log.Warn().Msg("[Ingestion] IngestCh 阻塞，丢弃了一帧 2D 栅格地图")
	}
}
