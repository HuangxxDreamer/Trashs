import { ref, shallowRef, onUnmounted } from 'vue';
import * as Cesium from 'cesium';

export function usePointCloud() {
  // 使用 shallowRef 避免 Vue 对大型 Cesium 对象进行深度响应式追踪，极大提升性能
  const scene = shallowRef<Cesium.Scene | null>(null);
  const localToWorldMatrix = shallowRef<Cesium.Matrix4 | null>(null);

  // 滑动窗口队列，存储点云集合
  const chunkQueue: Cesium.PointPrimitiveCollection[] = [];
  const MAX_CHUNKS = 500;

  // 性能指标
  const currentChunkCount = ref(0);
  const fps = ref(0);
  const droppedFrames = ref(0);

  // FPS 计算相关
  let lastTime = performance.now();
  let frameCount = 0;
  let rafId: number | null = null;

  /**
   * 初始化引擎引用
   */
  const initPointCloudEngine = (cesiumScene: Cesium.Scene, matrix: Cesium.Matrix4) => {
    scene.value = cesiumScene;
    localToWorldMatrix.value = matrix;
    console.log('[PointCloud] 引擎已初始化，modelMatrix 已存储:', matrix);
    startFpsCounter();
  };

  /**
   * 处理接收到的二进制点云帧
   * 数据结构: [x, y, z, r, g, b, a, ...] (Float32Array)
   */
  const processPointCloudFrame = (buffer: ArrayBuffer) => {
    if (!scene.value || !localToWorldMatrix.value) {
      console.warn('[PointCloud] 场景或 matrix 未就绪，丢弃帧');
      return;
    }

    try {
      const data = new Float32Array(buffer);
      const pointsCount = data.length / 7;

      console.log(`[PointCloud] 渲染 ${pointsCount} 个点，matrix 已应用`);

      // 创建一个新的点云集合（Chunk）
      const points = new Cesium.PointPrimitiveCollection({
        modelMatrix: localToWorldMatrix.value,
        blendOption: Cesium.BlendOption.OPAQUE
      });

      for (let i = 0; i < pointsCount; i++) {
        const offset = i * 7;
        points.add({
          position: new Cesium.Cartesian3(data[offset], data[offset + 1], data[offset + 2]),
          color: new Cesium.Color(
            data[offset + 3] / 255,
            data[offset + 4] / 255,
            data[offset + 5] / 255,
            data[offset + 6] / 255
          ),
          pixelSize: 8,
          scaleByDistance: new Cesium.NearFarScalar(1.0, 8.0, 200.0, 2.0)
        });
      }

      scene.value.primitives.add(points);
      scene.value.requestRender();
      chunkQueue.push(points);
      currentChunkCount.value = chunkQueue.length;

      // 滑动窗口：超过上限移除旧数据（防止 OOM）
      if (chunkQueue.length > MAX_CHUNKS) {
        const oldestChunk = chunkQueue.shift();
        if (oldestChunk && !oldestChunk.isDestroyed()) {
          scene.value.primitives.remove(oldestChunk);
        }
        droppedFrames.value++;
      }
    } catch (err) {
      console.error('[PointCloud] 处理点云帧失败:', err);
    }
  };

  /**
   * 启动 FPS 计数器
   */
  const startFpsCounter = () => {
    const update = () => {
      const now = performance.now();
      frameCount++;
      
      if (now - lastTime >= 1000) {
        fps.value = Math.round((frameCount * 1000) / (now - lastTime));
        frameCount = 0;
        lastTime = now;
      }
      rafId = requestAnimationFrame(update);
    };
    rafId = requestAnimationFrame(update);
  };

  onUnmounted(() => {
    if (rafId) cancelAnimationFrame(rafId);
    // 清理所有 Cesium 资源
    chunkQueue.forEach(chunk => {
      if (!chunk.isDestroyed()) chunk.destroy();
    });
    chunkQueue.length = 0;
  });

  return {
    currentChunkCount,
    fps,
    droppedFrames,
    initPointCloudEngine,
    processPointCloudFrame
  };
}
