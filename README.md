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
│   ├── ingestion/          # ROS2 Humble 数据接入层，高速读取二进制流 (rclgo)
│   ├── metrics/            # Prometheus 性能指标监控
│   ├── pool/               # 内存池管理，零分配核心
│   ├── processing/         # 3D 点云抽稀清洗与 2D 栅格压缩
│   ├── types/              # 核心数据结构定义
│   └── webrtc/             # WebRTC 传输、信令服务及抗拥塞控制
├── pkg/
│   └── config/             # 配置管理 (Viper)
├── .env.example            # 环境变量配置模板
├── scripts/
│   ├── save_pcd.py           # PCD 捕获脚本（不依赖 pcl_ros）
│   └── pcd_to_3dtiles.py     # PCD → 3D Tiles 转换（零依赖，替代 py3dtiles）
├── go.mod                  # 依赖管理
└── README.md               # 部署与使用文档
```

## 编译与运行

### 1. 环境准备

确保已安装 Go 1.22+，并在本地配置好了 **ROS2 Humble** 环境（需要 source `/opt/ros/humble/setup.bash` 以支持 rclgo 的 CGO 编译）。

```bash
# 下载依赖并拉取所有库
go mod tidy
```

### 2. 配置文件设置

```bash
# 复制示例配置
cp .env.example .env
```

请根据实际环境修改 `.env` 中的 `ROS_DOMAIN_ID` 等配置。

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

## “流媒体预览 + SSD 全量归档”双轨制

本项目实现了“前端实时增量预览”与“后端离线全量归档”的解耦架构。

### 1. 结束建图与归档流程

前端在完成建图后，可以通过 WebSocket 发送指令触发后端归档：
- **指令格式**：`{"action": "finish_mapping"}`
- **后端动作**（全异步执行）：
    1. **保存地图**：将最新接收到的 2D 栅格地图保存为 `./saved_maps/map_<timestamp>.png`。
    2. **导出 PCD**：调用 `scripts/save_pcd.py` 订阅 ROS 2 话题，捕获一帧点云并保存为 ASCII PCD 文件 `./saved_maps/cloud_<timestamp>.pcd`。
    3. **生成 3D Tiles**：自动调用 `scripts/pcd_to_3dtiles.py` 将 PCD 转换为 Cesium 可直接加载的 3D Tiles 格式（`tileset.json` + `.pnts`），存储于 `./saved_maps/3dtiles_<timestamp>/`。

### 2. 离线回放与访问

- **静态服务**：后端自动将 `./saved_maps/` 目录挂载到 `http://localhost:8080/maps/`。
- **Cesium 加载**：前端可以通过 `http://localhost:8080/maps/3dtiles_<timestamp>/tileset.json` 加载离线生成的 3D 地图。

### 3. PCD 导出脚本 (scripts/save_pcd.py)

自包含的 Python 脚本，仅依赖 ROS 2 基础包（`rclpy`, `sensor_msgs`, `numpy`），无需安装 `pcl_ros`。

```bash
# 用法
python3 scripts/save_pcd.py <topic> <output_path>

# 示例：订阅 /rtabmap/cloud_map，保存一帧到 saved_maps/
python3 scripts/save_pcd.py /rtabmap/cloud_map ./saved_maps/cloud_20260505_170000.pcd

# 脚本行为：
# 1. 订阅指定话题
# 2. 等待第一帧 PointCloud2 消息（30s 超时由 Go 侧控制）
# 3. 自动解析字段映射 (x/y/z + rgb/intensity)
# 4. 写入 ASCII PCD v0.7 格式
# 5. 退出
```

### 4. 3D Tiles 转换脚本 (scripts/pcd_to_3dtiles.py)

零依赖 Python 脚本，直接将 ASCII PCD 转换为 3D Tiles 1.0 格式，不依赖 `py3dtiles` 或 `numba`。

```bash
# 用法
python3 scripts/pcd_to_3dtiles.py <input.pcd> <output_dir>

# 示例
python3 scripts/pcd_to_3dtiles.py cloud.pcd ./3dtiles_output
# → ./3dtiles_output/tileset.json
# → ./3dtiles_output/points.pnts
```

输出为单文件 `.pnts` (Point Cloud) + `tileset.json`，Cesium 可直接通过 `tileset.json` 加载。

### 5. 配置说明

在 `.env` 中可以配置以下参数：
- `ARCHIVE_SAVE_DIR=saved_maps`：文件保存目录。
- `ARCHIVE_ROS_EXPORT_CMD="python3 scripts/save_pcd.py /rtabmap/cloud_map"`：PCD 导出命令。Go 运行时会自动追加输出路径参数。
- `ARCHIVE_TIMEOUT=300`：3D Tiles 转换的超时时间（秒）。PCD 导出独立使用 30s 超时。

## 优雅退出机制

当按下 `Ctrl+C` 时，网关会进入优雅关闭流程：
1. 发送信号给所有子 Goroutine 停止工作。
2. **等待归档任务**：主程序会使用 `sync.WaitGroup` 阻塞，直到当前正在运行的 `py3dtiles` 转换任务安全完成，防止生成损坏的数据集。
3. 打印“系统已安全关闭”后退出。
