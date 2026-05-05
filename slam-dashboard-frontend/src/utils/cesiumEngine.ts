import * as Cesium from 'cesium';

/**
 * 初始化 Cesium Viewer
 * @param containerId 容器 ID
 */
export function createCesiumViewer(containerId: string): Cesium.Viewer {
  const viewer = new Cesium.Viewer(containerId, {
    baseLayer: false, 
    // 2. 关闭其他不需要的全球环境特效（贴合地下 SLAM 场景）
    skyAtmosphere: false,   // 关闭大气层
    skyBox: false,          // 关闭天空盒（星空）
    globe: false,
    animation: false, // 禁用动画控件
    baseLayerPicker: false, // 禁用底图选择器
    fullscreenButton: false, // 禁用全屏按钮
    vrButton: false, // 禁用 VR 按钮
    geocoder: false, // 禁用地理编码搜索
    homeButton: false, // 禁用主页按钮
    infoBox: false, // 禁用信息框
    sceneModePicker: false, // 禁用 2D/3D 模式切换
    selectionIndicator: false, // 禁用选中指示器
    timeline: false, // 禁用时间轴
    navigationHelpButton: false, // 禁用帮助按钮
    scene3DOnly: true, // 仅支持 3D
    shouldAnimate: true,
    requestRenderMode: true, // 开启请求渲染模式，降低 CPU 消耗
    maximumRenderTimeChange: Infinity,
    contextOptions: {
      webgl: {
        preserveDrawingBuffer: true,
      },
    },
  });

  // 禁用地球和大气效果，打造纯粹的机器人坐标系空间

  viewer.scene.backgroundColor = Cesium.Color.fromCssColorString('#020617'); // Tailwind slate-950

  // 开启抗锯齿
  if (Cesium.FeatureDetection.supportsImageRenderingPixelated()) {
    viewer.resolutionScale = window.devicePixelRatio;
  }
  viewer.scene.postProcessStages.fxaa.enabled = true;

  // 隐藏版权信息
  const creditContainer = viewer.cesiumWidget.creditContainer as HTMLElement;
  if (creditContainer) {
    creditContainer.style.display = 'none';
  }

  return viewer;
}

/**
 * 获取“东北天”(ENU) 局部坐标系到世界坐标系的转换矩阵
 * 通常将机器人起始位置设为 [0, 0, 0]
 */
export function getDefaultLocalToWorldMatrix(): Cesium.Matrix4 {
  // 我们使用一个虚拟的经纬度位置作为参考点
  const origin = Cesium.Cartesian3.fromDegrees(120.0, 30.0, 0.0);
  return Cesium.Transforms.eastNorthUpToFixedFrame(origin);
}
