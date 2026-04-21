package types

import "time"

// DataType 定义了我们要处理的传感数据类型
type DataType int

const (
	DataTypePointCloud DataType = iota + 1 // 3D 点云
	DataTypeGridMap                        // 2D 栅格地图
)

// RosRawFrame 代表从 ROS Ingestion 层直接接收到的原始二进制数据帧
// 该结构体用于从 ingestCh 传递到 processing 层
// 为了追求极致性能，RawData 指向的内存是由 ROS 库底层分配的，或者我们在 Pool 中复用的 []byte
type RosRawFrame struct {
	Type      DataType      // 数据类型：点云或栅格地图
	RawData   []byte        // 原始二进制流
	Width     int           // 仅在 DataTypeGridMap 时有效：地图宽度
	Height    int           // 仅在 DataTypeGridMap 时有效：地图高度
	Timestamp time.Time     // 消息时间戳
}

// ProcessedFrame 代表经过清洗、滤波、坐标转换和压缩后的准备发送给前端的数据
// 该结构体用于从 processCh 传递到 webrtc 层
type ProcessedFrame struct {
	Type      DataType      // 数据类型
	Points    []float32     // 降采样后的 3D 点云（XYZ + RGBA 交错排列），仅在 Type == DataTypePointCloud 时有效
	MapData   []byte        // 压缩后的 PNG/WebP 2D 地图字节流，仅在 Type == DataTypeGridMap 时有效
	Timestamp time.Time     // 数据时间戳，用于前端同步
}
