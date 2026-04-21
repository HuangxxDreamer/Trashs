import { ref, onUnmounted, computed } from 'vue';

interface WebRTCOptions {
  onPointCloudData?: (data: ArrayBuffer) => void;
  onGridMapData?: (data: string) => void;
}

export function useWebRTC(options: WebRTCOptions) {
  const wsUrl = import.meta.env.VITE_WS_URL || 'ws://localhost:8080/ws';
  
  const connectionState = ref<RTCPeerConnectionState>('new');
  const isConnected = computed(() => connectionState.value === 'connected');
  const reconnectCount = ref(0);
  const maxReconnects = 5;
  
  let pc: RTCPeerConnection | null = null;
  let ws: WebSocket | null = null;
  let pcDataChannelPointCloud: RTCDataChannel | null = null;
  let pcDataChannelGridMap: RTCDataChannel | null = null;

  /**
   * 初始化 WebRTC 连接
   */
  const initConnection = () => {
    console.log(`[WebRTC] 正在连接信令服务器: ${wsUrl}`);
    ws = new WebSocket(wsUrl);

    ws.onopen = async () => {
      console.log('[WebRTC] 信令 WebSocket 已连接，正在创建 Offer...');
      await createOffer();
    };

    ws.onmessage = async (event) => {
      const msg = JSON.parse(event.data);
      
      if (msg.type === 'answer') {
        console.log('[WebRTC] 收到 Answer');
        await pc?.setRemoteDescription(new RTCSessionDescription(msg));
      } else if (msg.type === 'candidate') {
        console.log('[WebRTC] 收到 ICE Candidate');
        await pc?.addIceCandidate(new RTCIceCandidate(msg.candidate));
      }
    };

    ws.onclose = () => {
      console.warn('[WebRTC] 信令 WebSocket 已关闭');
      handleReconnect();
    };

    ws.onerror = (err) => {
      console.error('[WebRTC] WebSocket 错误:', err);
    };
  };

  /**
   * 创建 RTCPeerConnection 并发送 Offer
   */
  const createOffer = async () => {
    pc = new RTCPeerConnection({
      iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
    });

    // 监听连接状态
    pc.onconnectionstatechange = () => {
      connectionState.value = pc?.connectionState || 'failed';
      console.log(`[WebRTC] 连接状态变更: ${connectionState.value}`);
      if (connectionState.value === 'connected') {
        reconnectCount.value = 0;
      }
    };

    // 监听数据通道（如果是后端主动创建的话）
    pc.ondatachannel = (event) => {
      const channel = event.channel;
      setupDataChannel(channel);
    };

    // 浏览器作为 Offer 发起方，主动创建通道
    pcDataChannelPointCloud = pc.createDataChannel('pointcloud', {
      ordered: false,
      maxRetransmits: 0
    });
    pcDataChannelPointCloud.binaryType = 'arraybuffer';
    setupDataChannel(pcDataChannelPointCloud);

    pcDataChannelGridMap = pc.createDataChannel('gridmap', {
      ordered: true
    });
    setupDataChannel(pcDataChannelGridMap);

    // ICE Candidate 处理
    pc.onicecandidate = (event) => {
      if (event.candidate && ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
          type: 'candidate',
          candidate: event.candidate
        }));
      }
    };

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({
        type: 'offer',
        sdp: offer.sdp
      }));
    }
  };

  /**
   * 设置数据通道监听
   */
  const setupDataChannel = (channel: RTCDataChannel) => {
    console.log(`[WebRTC] 配置通道: ${channel.label}`);
    
    channel.onmessage = (event) => {
      if (channel.label === 'pointcloud') {
        options.onPointCloudData?.(event.data);
      } else if (channel.label === 'gridmap') {
        options.onGridMapData?.(event.data);
      }
    };

    channel.onopen = () => console.log(`[WebRTC] 通道 ${channel.label} 已开启`);
    channel.onclose = () => console.warn(`[WebRTC] 通道 ${channel.label} 已关闭`);
  };

  /**
   * 处理重连逻辑
   */
  const handleReconnect = () => {
    if (reconnectCount.value < maxReconnects) {
      reconnectCount.value++;
      console.log(`[WebRTC] 3秒后尝试第 ${reconnectCount.value} 次重连...`);
      setTimeout(initConnection, 3000);
    } else {
      console.error('[WebRTC] 达到最大重连次数，停止重连');
    }
  };

  const reconnect = () => {
    reconnectCount.value = 0;
    close();
    initConnection();
  };

  const close = () => {
    ws?.close();
    pc?.close();
    pc = null;
    ws = null;
  };

  onUnmounted(() => {
    close();
  });

  return {
    connectionState,
    isConnected,
    reconnect,
    initConnection
  };
}
