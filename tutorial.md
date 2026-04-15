# Dog Stream Gateway - 初学者复现与测试指南

欢迎来到 `dog-stream-gateway` 项目！这是一个基于 Go 语言开发的高性能机器狗数据网关，主要负责将 ROS 中的 3D 点云和 2D 栅格地图数据进行清洗、降采样，并通过 WebRTC 实时推送给前端（如 Cesium）。

本文档将手把手教你如何从零复现整个项目，并提供测试各个核心模块的方案。

---

## 目录
1. [项目架构与核心设计](#1-项目架构与核心设计)
2. [环境准备](#2-环境准备)
3. [项目复现步骤](#3-项目复现步骤)
4. [核心模块实现详解](#4-核心模块实现详解)
   - [4.1 共享类型与内存池 (types & pool)](#41-共享类型与内存池-types--pool)
   - [4.2 ROS 接入层 (Ingestion)](#42-ros-接入层-ingestion)
   - [4.3 数据清洗层 (Processing)](#43-数据清洗层-processing)
   - [4.4 WebRTC 流媒体层 (Egress)](#44-webrtc-流媒体层-egress)
   - [4.5 主程序组装 (main.go)](#45-主程序组装-maingo)
5. [核心模块测试方案](#5-核心模块测试方案)
   - [5.1 测试 ROS 接入层](#51-测试-ros-接入层)
   - [5.2 测试数据清洗层](#52-测试数据清洗层)
   - [5.3 测试 WebRTC 流媒体层](#53-测试-webrtc-流媒体层)
   - [5.4 测试系统监控](#54-测试系统监控)

---

## 1. 项目架构与核心设计

本网关的使命是**极致高性能**。我们不在这里做任何复杂的 SLAM 矩阵运算，只做一个纯粹的**生产者-消费者流媒体搬运工**。

### 核心设计思想：
1. **解耦**：分为 `Ingestion`（收）、`Processing`（洗）、`WebRTC`（发）三个独立模块。
2. **异步管道**：模块之间通过带缓冲的 Go Channel (`ingestCh` 和 `processCh`) 通信，上游只管推，下游只管取。
3. **零 GC 压力**：图像和点云是巨大的数组，绝不能在热路径（每秒10次的高频循环）中不断 `make()` 申请内存，必须使用 `sync.Pool` 进行复用。
4. **抗拥塞策略**：网络卡顿时，必须主动丢弃旧数据，保证前端拿到的永远是“最新的一帧”，避免 TCP 队头阻塞。

![架构示意图](https://via.placeholder.com/800x300.png?text=ROS+(goroslib)+-+IngestCh+->+Processing+(降采样)+-+ProcessCh+->+WebRTC+(DataChannel))

---

## 2. 环境准备

在开始之前，请确保你的开发环境满足以下要求：

- **操作系统**: Linux (推荐 Ubuntu 20.04/22.04) 或 Windows (配合 WSL2)
- **Go 语言**: 1.22 或更高版本
  - 验证命令: `go version`
- **ROS 2**: 推荐 Humble 版本（需要安装 C++ 的 ROS2 基础环境以支持 `rclgo` 的 CGO 编译）。
- **Git**: 用于代码版本控制

---

## 3. 项目复现步骤

### 3.1 克隆与初始化项目
如果你是从零开始，可以按以下步骤拉取或构建代码：

```bash
# 1. 创建并进入工作区
mkdir -p ~/My_Project/dog-stream-gateway
cd ~/My_Project/dog-stream-gateway

# 2. 如果你已有代码库，直接克隆（请替换为你的仓库地址）
git clone https://github.com/YourName/Trash.git slam_gateway
cd slam_gateway

# 3. 下载并整理 Go 依赖
go mod tidy
```

### 3.2 配置文件设置
项目使用 `.env` 文件来管理环境变量，确保你在运行前正确配置了它。

```bash
# 复制示例配置文件
cp .env.example .env
```

打开 `.env` 文件，初学者重点关注以下几项：
- `PORT=8080`: WebRTC 信令服务器和 Prometheus 监控端口。
- `ROS_DOMAIN_ID=0`: ROS2 的 Domain ID，确保与你的 ROS2 节点在同一个域。
- `ROS_NAMESPACE=""`: 节点命名空间，默认留空。

### 3.3 编译与运行

```bash
# 编译项目
go build -o dog-stream-gateway ./cmd/dog-stream-gateway/main.go

# 运行网关
./dog-stream-gateway
```
当你看到类似 `[Config] 全局配置加载成功` 和 `预分配内存池...` 的日志时，说明网关已成功启动！

## 4. 核心模块实现详解

为了让你不仅能跑起来，还能看懂代码是怎么写的，我们按数据流向详细拆解。

### 4.1 共享类型与内存池 (types & pool)
在 `internal/types/types.go` 中，我们定义了统一的帧结构：
- `RosRawFrame`：刚从 ROS 接到时的原始字节流。
- `ProcessedFrame`：经过清洗、降采样和压缩后，准备发给 WebRTC 的数据。

**为什么要有 `pool`？**
每秒有10帧甚至更高频的点云，每帧几万个点，如果每次都在堆上 `make([]float32)`，会引发极其频繁的 GC（垃圾回收），导致程序顿卡。
因此在 `internal/pool/pool.go` 中，我们使用了 `sync.Pool`：
- **设计**：`float32Pool` 预先分配了容量为 60000 的大数组。
- **使用**：借出时 `buf.Data = buf.Data[:0]`（重置长度但不销毁底层内存），归还时 `PutFloat32Buffer`，实现真正的**零内存分配**。

### 4.2 ROS 接入层 (Ingestion)
在 `internal/ingestion/ros.go` 中。
- **功能**：作为 ROS 网络中的一个节点，订阅 `/rtabmap/cloud_map` 和 `/rtabmap/grid_map`。
- **关键函数**：`onPointCloud2(msg *sensor_msgs.PointCloud2)`。
- **抗背压设计**：
  ```go
  select {
  case im.ingestCh <- frame:
  default:
      // 如果下游处理不过来（通道满了），直接丢弃这帧数据，绝不阻塞 ROS 节点本身
  }
  ```

### 4.3 数据清洗层 (Processing)
在 `internal/processing/processor.go` 中，跑在一个常驻的 Goroutine 里。
- **功能**：消费 `ingestCh` 传来的数据。
- **处理 3D 点云 (`handlePointCloud`)**：
  - 从 `pool` 借出 `[]float32`。
  - 使用**固定步长采样**（代替复杂的体素滤波）直接把二进制数据解析并存入浮点数组，控制点数在 8000 内。
  - 执行极简的平移操作模拟坐标系转换。
- **处理 2D 地图 (`handleGridMap`)**：
  - 将 `int8` 数组转为图片。
  - 用 `disintegration/imaging` 降采样（例如缩放到 200x200）。
  - 压缩为 PNG 并转成 Base64 字节流。
- 处理完后推入 `processCh`。

### 4.4 WebRTC 流媒体层 (Egress)
在 `internal/webrtc/sender.go` 中。
- **功能**：起一个 HTTP 服务暴露 WebSocket 用于信令（交换 SDP 握手），握手成功后建立 2 个 P2P 的 `DataChannel`。
- **通道设计**：
  - `dc3D`（点云通道）：配置 `Ordered: false` 和 `MaxRetransmits: 0`。这意味着它允许乱序和丢包，就像 UDP 一样，追求最低延迟。
  - `dc2D`（地图通道）：可靠传输。
- **工业级抗拥塞 (`webrtcSenderGoroutine`)**：
  ```go
  buffered := s.dc3D.BufferedAmount()
  if buffered > thresholdBytes {
      // 积压超过阈值（如 500KB），立即丢弃最新帧，防止客户端延迟越来越大
      s.freeFrame(frame)
      continue
  }
  ```

### 4.5 主程序组装 (main.go)
在 `cmd/dog-stream-gateway/main.go` 中。
- 初始化日志和 Viper 配置读取。
- 调用 `pool.PreAllocate(30)` 提前把池子塞满。
- `make(chan ..., 30)` 创建带缓冲的通道。
- 分别起 3 个后台 Goroutine 跑 Ingestion、Processing 和 WebRTC。
- 监听 `SIGINT/SIGTERM` 信号，配合 `context.WithCancel` 实现优雅退出。

---

## 5. 核心模块测试方案

为了确保系统稳定，我们需要分模块验证其功能。

### 5.1 测试 ROS 接入层

**目的**：验证网关能否成功连接到 ROS Master 并接收 PointCloud2 和 OccupancyGrid 消息。

**测试步骤**：
1. **启动网关**：
   在项目目录下运行 `./dog-stream-gateway`。
   此时你应该能看到日志提示：`[Ingestion] 已订阅 PointCloud2`。
2. **模拟发布 ROS2 数据**：
   再打开一个终端，使用 ROS2 命令行工具发布空数据：
   ```bash
   ros2 topic pub /rtabmap/cloud_map sensor_msgs/msg/PointCloud2 "{header: {stamp: {sec: 0, nanosec: 0}, frame_id: 'map'}, height: 1, width: 1, fields: [], is_bigendian: false, point_step: 16, row_step: 16, data: [0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0], is_dense: true}" -r 1
   ```
4. **观察日志**：
   正常情况下，网关不会报错。如果你想看明显的现象，可以在 `internal/ingestion/ros.go` 的 `onPointCloud2` 函数中临时加一句 `log.Info().Msg("收到一帧点云数据")`，重新编译运行即可看到源源不断的数据流入。

### 5.2 测试数据清洗层

**目的**：验证数据是否被正确降采样、转换以及 PNG 压缩。

**测试步骤**：
1. 这个模块属于纯计算模块，最适合使用 Go 的**单元测试**来验证。
2. 在 `internal/processing` 目录下创建一个 `processor_test.go`。
3. **测试点云降采样**：
   构造一个包含 20000 个点的 `[]byte` 数组传入 `handlePointCloud`，然后检查输出的 `ProcessedFrame.Points` 长度是否被正确限制在 `.env` 配置的 `MAX_POINTS`（默认 8000）以内。
4. **测试 2D 地图压缩**：
   构造一个全为 `0` 或 `100` 的 `[]byte` 数组传入 `handleGridMap`，检查输出的 `MapData` 是否为有效的 Base64 字符串，且能被反解为 PNG 图片。

*(注：项目追求极致性能，处理过程使用了零 GC 内存池，测试时请务必留意是否引发了 `panic: index out of range`)*

### 5.3 测试 WebRTC 流媒体层

**目的**：验证前后端能否成功建立 P2P 连接，并测试“拥塞丢帧”策略。

**测试步骤**：
1. **启动网关**：`./dog-stream-gateway`
2. **编写简易前端客户端**：
   新建一个简单的 `index.html`，使用浏览器的 WebRTC API 连接网关：
   ```html
   <script>
     const ws = new WebSocket('ws://127.0.0.1:8080/ws');
     const pc = new RTCPeerConnection();
     
     pc.ondatachannel = (event) => {
       console.log('收到 DataChannel:', event.channel.label);
       event.channel.onmessage = (e) => {
         console.log('收到数据大小:', e.data.byteLength);
       };
     };

     ws.onmessage = async (event) => {
       const offer = JSON.parse(event.data);
       await pc.setRemoteDescription(offer);
       const answer = await pc.createAnswer();
       await pc.setLocalDescription(answer);
       ws.send(JSON.stringify(answer));
     };
   </script>
   ```
3. **验证连通性**：在浏览器打开该 HTML，按 F12 查看控制台。如果打印了 `收到 DataChannel: pointcloud`，说明 WebRTC 握手成功！
4. **验证拥塞策略**：
   - 降低 `.env` 中的 `BUFFER_THRESHOLD_KB` 到 `1`（1KB）。
   - 使用 3.1 节的方法疯狂向 ROS 发布庞大的点云数据。
   - 观察网关日志，你应该能看到：`[WebRTC] 3D DataChannel 拥塞，触发主动丢帧策略`，这证明系统的抗干扰机制生效了！

### 5.4 测试系统监控

**目的**：验证 Prometheus 监控指标是否正常暴露。

**测试步骤**：
1. 确保网关正在运行。
2. 打开浏览器，访问 `http://127.0.0.1:8080/metrics`。
3. 搜索页面内容，你应该能找到自定义的指标，例如：
   - `gateway_pointcloud_frames_processed_total`
   - `gateway_webrtc_buffer_bytes`
   - `gateway_dropped_frames_total`
   - `gateway_memory_pool_borrow_total`
4. 随着程序的运行和数据的处理，刷新页面，你会看到这些数值在不断增加/变化，这说明零 GC 内存池和丢帧策略都在正常工作。

---

**祝你学习愉快！** 如果遇到任何问题，可以随时回头查看代码中详尽的中文注释。
