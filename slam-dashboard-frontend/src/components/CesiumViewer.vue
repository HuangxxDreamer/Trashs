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
    
    // 设置初始相机位置
    viewer.camera.setView({
      destination: Cesium.Cartesian3.fromDegrees(120.0, 30.0, 50.0), // 俯视
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
