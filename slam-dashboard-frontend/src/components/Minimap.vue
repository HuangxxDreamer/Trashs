<script setup lang="ts">
defineProps<{
  mapData: string; // Base64 PNG 字符串
}>();
</script>

<template>
  <div class="fixed bottom-6 left-6 w-64 h-64 bg-slate-900/80 border border-slate-700 rounded-lg backdrop-blur-md overflow-hidden shadow-2xl flex flex-col">
    <div class="px-3 py-1 bg-slate-800/50 border-b border-slate-700 flex justify-between items-center">
      <span class="text-xs font-mono text-slate-400 uppercase tracking-wider">2D Grid Map</span>
      <div class="w-2 h-2 rounded-full bg-cyan-400 animate-pulse"></div>
    </div>
    <div class="flex-1 flex items-center justify-center p-2 relative">
      <img 
        v-if="mapData" 
        :src="mapData" 
        class="max-w-full max-h-full object-contain image-pixelated"
        alt="Grid Map"
      />
      <div v-else class="text-slate-600 text-xs font-mono">WAITING FOR DATA...</div>
      
      <!-- 扫描线效果 -->
      <div class="absolute inset-0 pointer-events-none bg-scanlines opacity-10"></div>
    </div>
  </div>
</template>

<style scoped>
.image-pixelated {
  image-rendering: pixelated;
}

.bg-scanlines {
  background: linear-gradient(
    to bottom,
    transparent 50%,
    rgba(100, 255, 218, 0.1) 50%
  );
  background-size: 100% 4px;
}
</style>
