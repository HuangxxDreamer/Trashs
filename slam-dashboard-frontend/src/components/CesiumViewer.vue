<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue';
import { createCesiumViewer, getDefaultLocalToWorldMatrix } from '../utils/cesiumEngine';
import * as Cesium from 'cesium';

const emit = defineEmits<{
  (e: 'ready', payload: { scene: Cesium.Scene; matrix: Cesium.Matrix4 }): void;
}>();

const containerRef = ref<HTMLDivElement | null>(null);
let viewer: Cesium.Viewer | null = null;

onMounted(() => {
  if (containerRef.value) {
    viewer = createCesiumViewer(containerRef.value);
    const matrix = getDefaultLocalToWorldMatrix();

    // 点云实际位置约在局部坐标 (100~140, 164~235, 54~76)，相机置于点云中心上方俯瞰
    const cameraLocal = new Cesium.Cartesian3(120.0, 200.0, 200.0);
    const cameraWorld = Cesium.Matrix4.multiplyByPoint(matrix, cameraLocal, new Cesium.Cartesian3());
    viewer.camera.setView({
      destination: cameraWorld,
      orientation: {
        heading: Cesium.Math.toRadians(0),
        pitch: Cesium.Math.toRadians(-90),
        roll: 0
      }
    });

    emit('ready', {
      scene: viewer.scene,
      matrix: matrix
    });
  }
});

onUnmounted(() => {
  viewer?.destroy();
});
</script>

<template>
  <div ref="containerRef" class="w-full h-full relative overflow-hidden bg-slate-950" />
</template>

<style scoped>
:deep(.cesium-viewer) {
  width: 100%;
  height: 100%;
}
</style>
