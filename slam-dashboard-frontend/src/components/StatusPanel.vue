<script setup lang="ts">
defineProps<{
  connectionState: string;
  fps: number;
  chunkCount: number;
  droppedFrames: number;
}>();

const getStatusColor = (state: string) => {
  switch (state) {
    case 'connected': return 'text-emerald-400';
    case 'connecting': return 'text-amber-400';
    case 'failed': return 'text-rose-500';
    default: return 'text-slate-400';
  }
};
</script>

<template>
  <div class="fixed top-6 right-6 w-72 bg-slate-900/80 border border-slate-700 rounded-lg backdrop-blur-md p-4 shadow-2xl font-mono">
    <div class="text-xs text-slate-500 mb-4 flex justify-between items-center border-b border-slate-800 pb-2">
      <span>SYSTEM MONITOR</span>
      <span class="text-[10px] opacity-50">v1.0.0-PROD</span>
    </div>

    <div class="space-y-4">
      <!-- Connection Status -->
      <div class="flex justify-between items-end">
        <span class="text-xs text-slate-400">CONNECT_STATE</span>
        <span class="text-sm font-bold uppercase" :class="getStatusColor(connectionState)">
          {{ connectionState }}
        </span>
      </div>

      <!-- Performance Metrics -->
      <div class="grid grid-cols-2 gap-4">
        <div class="flex flex-col">
          <span class="text-[10px] text-slate-500">FPS_RENDER</span>
          <span class="text-lg font-bold text-cyan-400">{{ fps }}</span>
        </div>
        <div class="flex flex-col">
          <span class="text-[10px] text-slate-500">CHUNK_QUEUE</span>
          <span class="text-lg font-bold text-cyan-400">{{ chunkCount }}</span>
        </div>
      </div>

      <!-- Error Stats -->
      <div class="pt-2 border-t border-slate-800/50">
        <div class="flex justify-between text-[10px]">
          <span class="text-slate-500">DROPPED_CHUNKS</span>
          <span class="text-rose-400 font-bold">{{ droppedFrames }}</span>
        </div>
        <div class="w-full bg-slate-800 h-1 mt-1 rounded-full overflow-hidden">
          <div 
            class="bg-rose-500 h-full transition-all duration-500" 
            :style="{ width: `${Math.min((droppedFrames / 1000) * 100, 100)}%` }"
          ></div>
        </div>
      </div>
    </div>

    <!-- 底部装饰装饰 -->
    <div class="mt-4 flex gap-1">
      <div v-for="i in 12" :key="i" class="h-1 flex-1 bg-slate-800 rounded-sm" :class="{ 'bg-cyan-900/50': i < 5 }"></div>
    </div>
  </div>
</template>
