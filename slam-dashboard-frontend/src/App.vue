<script setup lang="ts">
import { ref } from 'vue';
import CesiumViewer from './components/CesiumViewer.vue';
import StatusPanel from './components/StatusPanel.vue';
import Minimap from './components/Minimap.vue';
import { useWebRTC } from './composables/useWebRTC';
import { usePointCloud } from './composables/usePointCloud';
import * as Cesium from 'cesium';

// 2D 栅格地图数据
const gridMapData = ref('');

// 点云管理逻辑
const { 
  currentChunkCount, 
  fps, 
  droppedFrames, 
  initPointCloudEngine, 
  processPointCloudFrame 
} = usePointCloud();

// WebRTC 通信逻辑
const {
  connectionState,
  initConnection,
  finishMapping
} = useWebRTC({
  onPointCloudData: (buffer) => {
    const data = new Float32Array(buffer);
    const pointCount = data.length / 7;
    const sizeKB = (buffer.byteLength / 1024).toFixed(2);
    console.log(
      `[PointCloud] 收到点云帧 | 字节数: ${buffer.byteLength} (${sizeKB} KB) | 点数: ${pointCount}`
    );

    // 打印前 3 个点用于快速验证数据流正确性
    const sampleCount = Math.min(3, pointCount);
    for (let i = 0; i < sampleCount; i++) {
      const off = i * 7;
      console.log(
        `[PointCloud] 点[${i}]: pos=(${data[off].toFixed(3)}, ${data[off + 1].toFixed(3)}, ${data[off + 2].toFixed(3)}) ` +
        `color=(${data[off + 3]}, ${data[off + 4]}, ${data[off + 5]}, ${data[off + 6]})`
      );
    }

    processPointCloudFrame(buffer);
  },
  onGridMapData: (data) => {
    // 判断一下如果后端没有加前缀，前端手动加上
    if (!data.startsWith('data:image')) {
      gridMapData.value = `data:image/png;base64,${data}`;
    } else {
      gridMapData.value = data;
    }
  }
});

/**
 * 当 Cesium Viewer 初始化完成时触发
 */
const handleCesiumReady = (payload: { scene: Cesium.Scene; matrix: Cesium.Matrix4 }) => {
  console.log('[App] Cesium 引擎就绪，正在初始化点云渲染器...');
  console.log('[App] 接收到的 matrix:', payload.matrix);
  console.log('[App] 接收到的 scene:', payload.scene);
  initPointCloudEngine(payload.scene, payload.matrix);

  // 引擎就绪后开始建立 WebRTC 连接
  initConnection();
};
</script>

<template>
  <div class="relative w-full h-full bg-slate-950 select-none overflow-hidden">
    <!-- 背景渲染层 -->
    <CesiumViewer @ready="handleCesiumReady" />

    <!-- 顶部状态栏 -->
    <StatusPanel
      :connection-state="connectionState"
      :fps="fps"
      :chunk-count="currentChunkCount"
      :dropped-frames="droppedFrames"
      @finish-mapping="finishMapping"
    />

    <!-- 左下角 2D 栅格地图 -->
    <Minimap :map-data="gridMapData" />

    <!-- 工业风 UI 装饰：边框光晕 -->
    <div class="pointer-events-none absolute inset-0 border-[1px] border-cyan-500/10 shadow-[inset_0_0_50px_rgba(6,182,212,0.05)]"></div>
    
    <!-- 装饰性网格线 -->
    <div class="pointer-events-none absolute inset-0 bg-[linear-gradient(rgba(255,255,255,0.02)_1px,transparent_1px),linear-gradient(90deg,rgba(255,255,255,0.02)_1px,transparent_1px)] bg-[size:40px_40px]"></div>
  </div>
</template>

<style>
/* 全局样式确保撑满 */
#app {
  width: 100vw;
  height: 100vh;
}
</style>
