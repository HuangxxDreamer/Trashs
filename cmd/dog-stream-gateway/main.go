package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"dog-stream-gateway/internal/ingestion"
	"dog-stream-gateway/internal/pool"
	"dog-stream-gateway/internal/processing"
	"dog-stream-gateway/internal/types"
	"dog-stream-gateway/internal/webrtc"
	"dog-stream-gateway/pkg/config"
)

func main() {
	// 1. 初始化日志配置
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano})

	// 2. 加载全局配置
	config.LoadConfig()

	// 3. 初始化全局内存池，预分配 30 个缓冲区，确保启动时零 GC 压力
	log.Info().Msg("预分配内存池...")
	pool.PreAllocate(30)

	// 4. 初始化核心 Channels (使用带缓冲通道实现模块解耦与抗背压)
	ingestCh := make(chan *types.RosRawFrame, 30)
	processCh := make(chan *types.ProcessedFrame, 30)

	// 5. 注册 Prometheus 指标暴露接口
	http.Handle("/metrics", promhttp.Handler())

	// 6. 初始化三大核心模块
	ingestionLayer := ingestion.NewIngestionManager(ingestCh)
	processingLayer := processing.NewProcessor(ingestCh, processCh)
	webrtcLayer, err := webrtc.NewWebRTCSender(processCh)
	if err != nil {
		log.Fatal().Err(err).Msg("初始化 WebRTC 发送层失败")
	}

	// 7. 使用 Context 管理优雅退出
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动 Processing 消费者
	go processingLayer.Start(ctx)

	// 启动 WebRTC 信令与发送层
	go webrtcLayer.Start(ctx)

	// 启动 ROS 接入层 (如果在前台阻塞运行则不加 go，但为了响应信号，我们放到后台)
	go func() {
		if err := ingestionLayer.Start(ctx); err != nil {
			log.Error().Err(err).Msg("ROS 接入层异常退出")
			cancel() // 关键模块退出时，触发全局退出
		}
	}()

	// 8. 监听系统中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 阻塞等待退出信号
	sig := <-sigCh
	log.Info().Str("Signal", sig.String()).Msg("收到系统信号，开始优雅关闭网关...")

	// 触发所有子模块的退出
	cancel()

	// 预留少量时间让 Goroutine 善后（例如释放 UDP 端口，关闭节点等）
	time.Sleep(1 * time.Second)
	log.Info().Msg("系统已安全关闭，再见！")
}
