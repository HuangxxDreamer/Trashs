# SLAM Dashboard Frontend (机器狗 SLAM 可视化前端)

这是一个专为机器狗 SLAM 设计的高性能前端可视化系统。基于 Vue 3 + TypeScript + Cesium 构建，通过 WebRTC 实现毫秒级延迟的点云和栅格地图传输。

## 🚀 核心特性

- **极致性能**：利用 `shallowRef` 和 Cesium `PointPrimitiveCollection` 批量渲染，支持数十万点云实时显示。
- **抗 OOM 防御**：内置滑动窗口内存管理，自动销毁过期点云 Chunk，确保浏览器长时间运行不崩溃。
- **WebRTC P2P**：通过 DataChannel 直接接收二进制流，绕过复杂的序列化开销。
- **工业级 UI**：基于 Tailwind CSS 打造的深色系工业风仪表盘。

## 🛠️ 技术栈

- **框架**: Vue 3.5+ (Composition API)
- **渲染**: Cesium 1.115+
- **构建**: Vite 6.x
- **通信**: WebRTC + WebSocket
- **样式**: Tailwind CSS

## 📦 快速开始

### 1. 安装依赖
```bash
npm install
```

### 2. 配置环境变量
在项目根目录创建 `.env` 文件：
```env
VITE_WS_URL=ws://localhost:8080/ws
VITE_CESIUM_ION_TOKEN=你的_CESIUM_TOKEN
```

### 3. 启动开发服务器
```bash
npm run dev
```

## 🔌 后端配合说明

后端 (dog-stream-gateway) 需满足以下协议：

1. **信令交互**：通过 WebSocket 交换标准 WebRTC Offer/Answer 和 ICE Candidates。
2. **DataChannels**：
   - `pointcloud`: `binaryType = 'arraybuffer'`。推送 `Float32Array`，格式为 `[x, y, z, r, g, b, a, ...]`，均为 32 位浮点数。
   - `gridmap`: 推送包含 Base64 图片数据的字符串（例如：`data:image/png;base64,...`）。

## ⚙️ 性能调优参数

在 `src/composables/usePointCloud.ts` 中可以调整以下参数：
- `MAX_CHUNKS`: 最大保留的点云块数量（默认 500）。调大可查看更长的历史轨迹，但会增加内存占用。
- `pixelSize`: 点云像素大小（默认 3）。

## 📄 许可证
MIT
