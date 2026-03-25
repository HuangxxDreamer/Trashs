# Dog Stream Gateway (机器狗 Go 后端数据网关)

这是一个 **极致高性能** 的机器狗数据网关，基于 **生产者-消费者流媒体模型** 构建。专门用于高速接入 Jetson 本地的 RTAB-Map ROS 数据，进行实时清洗、抽稀和坐标转换，并最终通过 WebRTC DataChannel 以最低延迟推流给远端的 Cesium 浏览器。

## 核心特性

- **零 GC 压力**：全局 `sync.Pool` 预分配内存，热路径上的 `[]float32` 和 `[]byte` 完全零分配。
- **工业级抗干扰**：彻底抛弃 TCP 队头阻塞，基于 WebRTC 的 SCTP over UDP 传输。
- **动态丢帧策略**：实时监控 DataChannel 的 `BufferedAmount`，拥塞时主动丢弃堆积点云帧，保证前端“永远处于现在”。
- **解耦架构**：通过 Channel 完美隔离 Ingestion、Processing 和 Egress 三层，支持异步抗背压。

## 目录结构

```text
dog-stream-gateway/
├── cmd/dog-stream-gateway/ # 程序入口，组装各模块，处理优雅退出
├── internal/
│   ├── ingestion/          # ROS1/ROS2 数据接入层，高速读取二进制流
│   ├── metrics/            # Prometheus 性能指标监控
│   ├── pool/               # 内存池管理，零分配核心
│   ├── processing/         # 3D 点云抽稀清洗与 2D 栅格压缩
│   ├── types/              # 核心数据结构定义
│   └── webrtc/             # WebRTC 传输、信令服务及抗拥塞控制
├── pkg/
│   └── config/             # 配置管理 (Viper)
├── .env.example            # 环境变量配置模板
├── go.mod                  # 依赖管理
└── README.md               # 部署与使用文档
```

## 编译与运行

### 1. 环境准备

确保已安装 Go 1.22+，并在本地具有 ROS1 Master 或者可以通过网络连接到远端 ROS 机器。

```bash
# 下载依赖并拉取所有库
go mod tidy
```

### 2. 配置文件设置

```bash
# 复制示例配置
cp .env.example .env
```

请根据实际环境修改 `.env` 中的 `ROS_MASTER_URI`、`ROS_NODE_HOST` 等配置。

### 3. 启动网关

```bash
# 运行项目
go run ./cmd/dog-stream-gateway/main.go

# 或者编译后运行
go build -o dog-stream-gateway ./cmd/dog-stream-gateway/main.go
./dog-stream-gateway
```

## 性能调优参数

可以在 `.env` 中修改以下参数：
- `BUFFER_THRESHOLD_KB=500`：WebRTC DataChannel 的积压阈值。若网络变差导致此值超标，系统将自动丢弃最新点云帧。
- `MAX_POINTS=8000`：单帧点云抽稀后的最大保留点数。降低该值可大幅减少传输带宽。

## 监控指标

程序启动后，默认在 `:8080/metrics` 暴露 Prometheus 监控端点，包含以下自定义指标：
- `gateway_pointcloud_frames_processed_total`: 点云帧处理总数（用于计算帧率）。
- `gateway_webrtc_buffer_bytes`: 当前 WebRTC 缓冲区的积压情况。
- `gateway_dropped_frames_total`: 主动丢弃的帧数（按丢弃原因分类）。
- `gateway_memory_pool_borrow_total`: 内存池借出频率，用于监控池的健康状态。

## ROS1 与 ROS2 切换说明

当前项目使用 `github.com/bluenviron/goroslib/v2` 作为 ROS1 的纯 Go 实现。

**如需切换至 ROS2：**
1. 移除 `goroslib` 相关依赖。
2. 引入 `github.com/tiiuae/cyclonedds-go` 或其他 CGO 桥接方案。
3. 仅需修改 `internal/ingestion/ros.go`：
   - 将 `NewNode` 替换为 ROS2 参与者的初始化逻辑。
   - 订阅 `/rtabmap/cloud_map` 时，将收到的 `[]byte` 继续封装为 `types.RosRawFrame`，并送入 `ingestCh` 即可，**无需修改任何下游代码**。
