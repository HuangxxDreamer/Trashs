package ingestion

import (
	"context"
	"time"

	"github.com/bluenviron/goroslib/v2"
	"github.com/bluenviron/goroslib/v2/pkg/msgs/nav_msgs"
	"github.com/bluenviron/goroslib/v2/pkg/msgs/sensor_msgs"
	"github.com/rs/zerolog/log"

	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/pkg/config"
)

// IngestionManager 是 ROS 数据接入层的核心管理器，负责：
// 1. 初始化 ROS 节点（支持 goroslib/v2）。
// 2. 订阅 /rtabmap/cloud_map 和 /rtabmap/grid_map 等话题。
// 3. 接收到消息后，进行极少量的解析和直接复制，并通过 ingestCh 推送到 Processing 层。
//
// 注：若后续需切换至 ROS2，在此文件中更换底层的 `goroslib` 实现即可，例如改用 `cyclonedds-go`，
// 只需要在此处的 OnCloud 和 OnGrid 回调中保留同样的 Channel 投递逻辑即可，不影响下游处理模块。
type IngestionManager struct {
	node     *goroslib.Node
	subCloud *goroslib.Subscriber
	subGrid  *goroslib.Subscriber
	ingestCh chan *types.RosRawFrame
}

// NewIngestionManager 构造一个新的 IngestionManager 实例，并关联数据下发的通道。
func NewIngestionManager(ingestCh chan *types.RosRawFrame) *IngestionManager {
	return &IngestionManager{
		ingestCh: ingestCh,
	}
}

// Start 负责真正启动 ROS1 节点，建立连接并开始订阅话题。
// 它是一个阻塞方法或需要使用 Goroutine 调用的。
// 使用 context.Context 使得系统能够随时中断连接。
func (im *IngestionManager) Start(ctx context.Context) error {
	var err error
	// 使用 goroslib/v2 启动节点
	im.node, err = goroslib.NewNode(goroslib.NodeConf{
		Name:          config.Cfg.ROS.NodeName,
		MasterAddress: config.Cfg.ROS.MasterURI,
		Host:          config.Cfg.ROS.NodeHost,
	})
	if err != nil {
		log.Error().Err(err).Msg("[Ingestion] 创建 ROS 节点失败")
		return err
	}
	log.Info().Str("Node", config.Cfg.ROS.NodeName).Msg("[Ingestion] ROS 节点创建成功")

	// 订阅 3D 点云（PointCloud2）
	im.subCloud, err = goroslib.NewSubscriber(goroslib.SubscriberConf{
		Node:     im.node,
		Topic:    config.Cfg.ROS.TopicCloud,
		Callback: im.onPointCloud2,
	})
	if err != nil {
		log.Error().Err(err).Str("Topic", config.Cfg.ROS.TopicCloud).Msg("[Ingestion] 订阅 PointCloud2 失败")
		return err
	}
	log.Info().Str("Topic", config.Cfg.ROS.TopicCloud).Msg("[Ingestion] 已订阅 PointCloud2")

	// 订阅 2D 栅格地图（OccupancyGrid）
	im.subGrid, err = goroslib.NewSubscriber(goroslib.SubscriberConf{
		Node:     im.node,
		Topic:    config.Cfg.ROS.TopicGrid,
		Callback: im.onOccupancyGrid,
	})
	if err != nil {
		log.Error().Err(err).Str("Topic", config.Cfg.ROS.TopicGrid).Msg("[Ingestion] 订阅 OccupancyGrid 失败")
		return err
	}
	log.Info().Str("Topic", config.Cfg.ROS.TopicGrid).Msg("[Ingestion] 已订阅 OccupancyGrid")

	// 等待上下文取消信号
	<-ctx.Done()
	log.Info().Msg("[Ingestion] 收到退出信号，正在关闭 ROS 节点...")
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
	log.Info().Msg("[Ingestion] ROS 节点已完全关闭")
}

// onPointCloud2 是传感器回调，接收到原始二进制数据。
// 注意：在这里我们仅做最低限度的转换，将 ROS 的 sensor_msgs/PointCloud2 包装为 RosRawFrame，
// 不做任何解码与点位遍历。我们直接传递底层 Data 切片的引用，以实现真正的零拷贝！
func (im *IngestionManager) onPointCloud2(msg *sensor_msgs.PointCloud2) {
	frame := &types.RosRawFrame{
		Type:      types.DataTypePointCloud,
		RawData:   msg.Data,
		Timestamp: time.Now(),
	}

	// 尝试写入通道，如果通道积压，不阻塞，避免影响底层 goroslib 的心跳与吞吐。
	select {
	case im.ingestCh <- frame:
	default:
		// 当 Processing 消费速度跟不上时，直接丢弃该帧数据。
		// 在高频率流媒体应用中，“宁可丢帧，绝不阻塞”是抗延迟的第一原则。
		log.Warn().Msg("[Ingestion] IngestCh 阻塞，丢弃了一帧 3D 点云")
	}
}

// onOccupancyGrid 栅格地图的回调。
func (im *IngestionManager) onOccupancyGrid(msg *nav_msgs.OccupancyGrid) {
	// ROS1 OccupancyGrid Data是 int8 数组，我们为了通用，使用 unsafe 转换或者简单的循环拷贝。
	// 但如果使用纯 Go 库，一般其类型是 []int8，需要转换为 []byte
	// 简单转换 []int8 到 []byte（因为都是 1 字节长），我们可以使用一个快速拷贝
	
	// 在高频中，也可以借助 unsafe 实现强转零拷贝。由于这里并非主热路径（2D更新频率一般比3D低），
	// 安全起见我们做一次内存拷贝。
	dataBytes := make([]byte, len(msg.Data))
	for i, v := range msg.Data {
		dataBytes[i] = byte(v)
	}

	frame := &types.RosRawFrame{
		Type:      types.DataTypeGridMap,
		RawData:   dataBytes,
		Timestamp: time.Now(),
	}

	select {
	case im.ingestCh <- frame:
	default:
		log.Warn().Msg("[Ingestion] IngestCh 阻塞，丢弃了一帧 2D 栅格地图")
	}
}
